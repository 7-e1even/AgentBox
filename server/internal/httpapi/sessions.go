package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	sandboxSessionTicketLifetime = 30 * time.Second
	sandboxSessionReadLimit      = 1024 * 1024
	sandboxSessionWriteTimeout   = 10 * time.Second
	sandboxSessionPingInterval   = 30 * time.Second
	sandboxSessionPingTimeout    = 10 * time.Second
)

type sandboxSessionTarget struct {
	SandboxID  string
	ServerID   string
	ExternalID string
	Driver     string
}

type sandboxSessionTicket struct {
	Target    sandboxSessionTarget
	ExpiresAt time.Time
}

type sessionMessage struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"sessionId,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
	SandboxID  string          `json:"sandboxId,omitempty"`
	ExternalID string          `json:"externalId,omitempty"`
	Driver     string          `json:"driver,omitempty"`
	Data       string          `json:"data,omitempty"`
	Operation  string          `json:"operation,omitempty"`
	Path       string          `json:"path,omitempty"`
	Content    string          `json:"content,omitempty"`
	UploadID   string          `json:"uploadId,omitempty"`
	Cols       int             `json:"cols,omitempty"`
	Rows       int             `json:"rows,omitempty"`
	OK         bool            `json:"ok,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type sessionSocket struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (s *sessionSocket) write(message []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), sandboxSessionWriteTimeout)
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageText, message)
}

func (s *sessionSocket) writeJSON(message sessionMessage) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.write(encoded)
}

// startSessionPing 每 30s 发送一次 ping 并等待 pong；超时未收到 pong 则关闭连接，
// 使阻塞中的 Read 返回错误，从而触发 defer 里的 hub 清理。
// coder/websocket v1.8 会在 Read 中自动回应对端 ping（read.go handleControl），
// 浏览器亦由协议栈自动回 pong，因此对端无需任何改动。
func startSessionPing(conn *websocket.Conn, done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(sandboxSessionPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), sandboxSessionPingTimeout)
				err := conn.Ping(ctx)
				cancel()
				if err != nil {
					_ = conn.Close(websocket.StatusGoingAway, "ping timeout")
					return
				}
			}
		}
	}()
}

type workerSessionConnection struct {
	serverID string
	socket   *sessionSocket
}

type browserSessionConnection struct {
	id       string
	serverID string
	socket   *sessionSocket
}

type sessionHub struct {
	mu             sync.RWMutex
	workers        map[string]*workerSessionConnection
	sessions       map[string]*browserSessionConnection
	tickets        map[string]sandboxSessionTicket
	originPatterns []string
	trustedProxy   bool
}

func newSessionHub(origins []string, trustedProxy bool) *sessionHub {
	patterns := make([]string, 0, len(origins))
	for _, origin := range origins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return &sessionHub{
		workers:        make(map[string]*workerSessionConnection),
		sessions:       make(map[string]*browserSessionConnection),
		tickets:        make(map[string]sandboxSessionTicket),
		originPatterns: patterns,
		trustedProxy:   trustedProxy,
	}
}

func (h *sessionHub) acceptOptions(request *http.Request) *websocket.AcceptOptions {
	patterns := append([]string(nil), h.originPatterns...)
	// 仅在信任代理时才把 X-Forwarded-Host 并入 Origin 白名单，防止伪造头绕过校验。
	if h.trustedProxy {
		if forwardedHost := forwardedHostPattern(request.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			patterns = append(patterns, forwardedHost)
		}
	}
	return &websocket.AcceptOptions{OriginPatterns: patterns}
}

func forwardedHostPattern(value string) string {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	if value == "" || strings.ContainsAny(value, `*/\?#@`) {
		return ""
	}
	parsed, err := url.Parse("http://" + value)
	if err != nil || parsed.Host != value || parsed.Hostname() == "" || parsed.Path != "" {
		return ""
	}
	return value
}

func (h *sessionHub) hasWorker(serverID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workers[serverID] != nil
}

func (h *sessionHub) issueTicket(target sandboxSessionTarget) (string, time.Time, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, fmt.Errorf("generate sandbox session ticket: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	expiresAt := time.Now().UTC().Add(sandboxSessionTicketLifetime)
	h.mu.Lock()
	defer h.mu.Unlock()
	for existing, ticket := range h.tickets {
		if time.Now().UTC().After(ticket.ExpiresAt) {
			delete(h.tickets, existing)
		}
	}
	h.tickets[token] = sandboxSessionTicket{Target: target, ExpiresAt: expiresAt}
	return token, expiresAt, nil
}

func (h *sessionHub) consumeTicket(token, sandboxID string) (sandboxSessionTicket, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ticket, ok := h.tickets[token]
	delete(h.tickets, token)
	if !ok || ticket.Target.SandboxID != sandboxID || time.Now().UTC().After(ticket.ExpiresAt) {
		return sandboxSessionTicket{}, false
	}
	return ticket, true
}

func (h *sessionHub) registerWorker(connection *workerSessionConnection) *workerSessionConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.workers[connection.serverID]
	h.workers[connection.serverID] = connection
	return previous
}

func (h *sessionHub) unregisterWorker(connection *workerSessionConnection) []*browserSessionConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.workers[connection.serverID] != connection {
		return nil
	}
	delete(h.workers, connection.serverID)
	disconnected := make([]*browserSessionConnection, 0)
	for id, session := range h.sessions {
		if session.serverID == connection.serverID {
			disconnected = append(disconnected, session)
			delete(h.sessions, id)
		}
	}
	return disconnected
}

func (h *sessionHub) registerBrowser(connection *browserSessionConnection) (*workerSessionConnection, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	worker := h.workers[connection.serverID]
	if worker == nil {
		return nil, false
	}
	h.sessions[connection.id] = connection
	return worker, true
}

func (h *sessionHub) unregisterBrowser(connection *browserSessionConnection) *workerSessionConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[connection.id] == connection {
		delete(h.sessions, connection.id)
	}
	return h.workers[connection.serverID]
}

func (h *sessionHub) browserForWorker(serverID, sessionID string) *browserSessionConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	session := h.sessions[sessionID]
	if session == nil || session.serverID != serverID {
		return nil
	}
	return session
}

func (s *Server) createSandboxSessionTicket(w http.ResponseWriter, request *http.Request) {
	target, err := s.resolveSandboxSessionTarget(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	if !s.sessions.hasWorker(target.ServerID) {
		s.writeError(w, http.StatusServiceUnavailable, "沙箱会话服务尚未连接，请先升级并重启目标 Worker")
		return
	}
	token, expiresAt, err := s.sessions.issueTicket(target)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"ticket": token, "expiresAt": expiresAt,
	})
}

func (s *Server) resolveSandboxSessionTarget(ctx context.Context, sandboxID string) (sandboxSessionTarget, error) {
	resources, err := s.store.ListResources(ctx)
	if err != nil {
		return sandboxSessionTarget{}, err
	}
	var sandbox *platform.Resource
	for index := range resources {
		if resources[index].Kind == platform.KindSandbox && resources[index].ID == sandboxID {
			sandbox = &resources[index]
			break
		}
	}
	if sandbox == nil {
		return sandboxSessionTarget{}, store.ErrResourceNotFound
	}
	status, _ := sandbox.Spec["status"].(string)
	if status != "running" {
		return sandboxSessionTarget{}, fmt.Errorf("%w: sandbox must be running", store.ErrConflict)
	}
	serverID, _ := sandbox.Spec["serverId"].(string)
	externalID, _ := sandbox.Spec["externalId"].(string)
	driver, _ := sandbox.Spec["driver"].(string)
	if driver == "" {
		runtimeID, _ := sandbox.Spec["runtimeId"].(string)
		for index := range resources {
			if resources[index].Kind == platform.KindRuntime && resources[index].ID == runtimeID {
				driver, _ = resources[index].Spec["driver"].(string)
				break
			}
		}
	}
	if serverID == "" || externalID == "" || driver == "" {
		return sandboxSessionTarget{}, fmt.Errorf("%w: sandbox session target is incomplete", store.ErrConflict)
	}
	if driver != "docker" && driver != "boxlite" && driver != "microsandbox" {
		return sandboxSessionTarget{}, fmt.Errorf("%w: unsupported sandbox session driver: %s", store.ErrConflict, driver)
	}
	return sandboxSessionTarget{SandboxID: sandboxID, ServerID: serverID, ExternalID: externalID, Driver: driver}, nil
}

func (s *Server) connectWorkerSessions(w http.ResponseWriter, request *http.Request) {
	serverID := request.PathValue("id")
	credential := authBearer(request)
	if err := s.store.HeartbeatServer(request.Context(), serverID, credential, nil, nil, ""); err != nil {
		if errors.Is(err, store.ErrWorkerUnauthorized) {
			s.writeError(w, http.StatusUnauthorized, "Worker authentication failed")
			return
		}
		s.handleError(w, err)
		return
	}
	conn, err := websocket.Accept(w, request, s.sessions.acceptOptions(request))
	if err != nil {
		s.logger.Warn("accept worker session websocket", "server_id", serverID, "error", err)
		return
	}
	conn.SetReadLimit(sandboxSessionReadLimit)
	pingDone := make(chan struct{})
	defer close(pingDone)
	startSessionPing(conn, pingDone)
	worker := &workerSessionConnection{serverID: serverID, socket: &sessionSocket{conn: conn}}
	if previous := s.sessions.registerWorker(worker); previous != nil {
		_ = previous.socket.conn.Close(websocket.StatusServiceRestart, "worker session replaced")
	}
	s.logger.Info("worker session connected", "server_id", serverID)
	defer func() {
		for _, session := range s.sessions.unregisterWorker(worker) {
			_ = session.socket.writeJSON(sessionMessage{Type: "error", Error: "Worker 会话已断开，正在等待重连"})
			_ = session.socket.conn.Close(websocket.StatusTryAgainLater, "worker disconnected")
		}
		worker.socket.conn.CloseNow()
		s.logger.Info("worker session disconnected", "server_id", serverID)
	}()

	for {
		_, payload, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var message sessionMessage
		if json.Unmarshal(payload, &message) != nil || message.SessionID == "" {
			continue
		}
		browser := s.sessions.browserForWorker(serverID, message.SessionID)
		if browser == nil {
			continue
		}
		if err := browser.socket.write(payload); err != nil {
			_ = browser.socket.conn.Close(websocket.StatusInternalError, "browser write failed")
		}
	}
}

func (s *Server) connectSandboxSession(w http.ResponseWriter, request *http.Request) {
	ticket, ok := s.sessions.consumeTicket(request.URL.Query().Get("ticket"), request.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "沙箱会话票据无效或已过期")
		return
	}
	conn, err := websocket.Accept(w, request, s.sessions.acceptOptions(request))
	if err != nil {
		s.logger.Warn("accept browser session websocket", "sandbox_id", ticket.Target.SandboxID, "error", err)
		return
	}
	conn.SetReadLimit(sandboxSessionReadLimit)
	pingDone := make(chan struct{})
	defer close(pingDone)
	startSessionPing(conn, pingDone)
	browser := &browserSessionConnection{
		id: uuid.NewString(), serverID: ticket.Target.ServerID, socket: &sessionSocket{conn: conn},
	}
	worker, ok := s.sessions.registerBrowser(browser)
	if !ok {
		_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
		return
	}
	defer func() {
		if currentWorker := s.sessions.unregisterBrowser(browser); currentWorker != nil {
			_ = currentWorker.socket.writeJSON(sessionMessage{Type: "close", SessionID: browser.id})
		}
		conn.CloseNow()
	}()
	if err := worker.socket.writeJSON(sessionMessage{
		Type: "open", SessionID: browser.id, SandboxID: ticket.Target.SandboxID,
		ExternalID: ticket.Target.ExternalID, Driver: ticket.Target.Driver, Cols: 120, Rows: 30,
	}); err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
		return
	}

	for {
		_, payload, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		message, ok := validBrowserSessionMessage(payload)
		if !ok {
			_ = browser.socket.writeJSON(sessionMessage{Type: "error", Error: "无效的会话消息"})
			continue
		}
		message.SessionID = browser.id
		if err := worker.socket.writeJSON(message); err != nil {
			_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
			return
		}
	}
}

func validBrowserSessionMessage(payload []byte) (sessionMessage, bool) {
	var message sessionMessage
	if len(payload) == 0 || len(payload) > sandboxSessionReadLimit || json.Unmarshal(payload, &message) != nil {
		return sessionMessage{}, false
	}
	switch message.Type {
	case "input":
		return message, len(message.Data) <= 64*1024
	case "resize":
		return message, message.Cols >= 2 && message.Cols <= 1000 && message.Rows >= 1 && message.Rows <= 500
	case "rpc":
		if message.RequestID == "" || len(message.RequestID) > 100 || message.Path == "" || len(message.Path) > 4096 {
			return sessionMessage{}, false
		}
		switch message.Operation {
		case "list", "read":
			return message, message.Content == "" && message.UploadID == ""
		case "write":
			return message, len(message.Content) <= 512*1024 && message.UploadID == ""
		case "upload-start", "upload-finish", "upload-cancel":
			return message, message.Content == "" && validUploadID(message.UploadID)
		case "upload-chunk":
			return message, len(message.Content) <= 384*1024 && validUploadID(message.UploadID)
		default:
			return sessionMessage{}, false
		}
	default:
		return sessionMessage{}, false
	}
}

func validUploadID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
