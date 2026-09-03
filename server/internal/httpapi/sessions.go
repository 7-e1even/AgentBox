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
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	// SessionTopology is intentionally not configurable: Worker/browser sockets
	// and single-use tickets belong to one Server process, not the database.
	SessionTopology = "single-instance"

	sandboxSessionTicketLifetime = 30 * time.Second
	sandboxSessionReadLimit      = 1024 * 1024
	sandboxSessionWriteTimeout   = 10 * time.Second
	sandboxWorkerKeepalive       = 20 * time.Second
)

var errSandboxDesktopNotEnabled = errors.New("sandbox desktop is not enabled")

type sandboxSessionTarget struct {
	SandboxID  string
	ServerID   string
	ExternalID string
	Driver     string
}

type sandboxSessionTicket struct {
	Target    sandboxSessionTarget
	Mode      string
	ExpiresAt time.Time
}

type sessionMessage struct {
	Type       string          `json:"type"`
	Mode       string          `json:"mode,omitempty"`
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
	Cols       int             `json:"cols,omitzero"`
	Rows       int             `json:"rows,omitzero"`
	OK         bool            `json:"ok,omitzero"`
	Retryable  bool            `json:"retryable,omitzero"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type sessionSocket struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (s *sessionSocket) write(messageType websocket.MessageType, message []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), sandboxSessionWriteTimeout)
	defer cancel()
	return s.conn.Write(ctx, messageType, message)
}

func (s *sessionSocket) writeJSON(message sessionMessage) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.write(websocket.MessageText, encoded)
}

func (s *sessionSocket) writeBinary(message []byte) error {
	return s.write(websocket.MessageBinary, message)
}

type workerSessionConnection struct {
	serverID string
	socket   *sessionSocket
}

type browserSessionConnection struct {
	id       string
	serverID string
	mode     string
	socket   *sessionSocket
}

type sessionHub struct {
	// All three maps are process-local. Server replicas and cross-instance
	// ticket routing are unsupported; restarting requires fresh connections.
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
	patterns := slices.Clone(h.originPatterns)
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

func (h *sessionHub) issueTicket(target sandboxSessionTarget, mode string) (string, time.Time, error) {
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
	h.tickets[token] = sandboxSessionTicket{Target: target, Mode: mode, ExpiresAt: expiresAt}
	return token, expiresAt, nil
}

func (h *sessionHub) consumeTicket(token, sandboxID, mode string) (sandboxSessionTicket, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ticket, ok := h.tickets[token]
	delete(h.tickets, token)
	if !ok || ticket.Target.SandboxID != sandboxID || ticket.Mode != mode || time.Now().UTC().After(ticket.ExpiresAt) {
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
	s.createSandboxConnectionTicket(w, request, "terminal")
}

func (s *Server) createSandboxDesktopTicket(w http.ResponseWriter, request *http.Request) {
	s.createSandboxConnectionTicket(w, request, "desktop")
}

func (s *Server) createSandboxConnectionTicket(w http.ResponseWriter, request *http.Request, mode string) {
	target, err := s.resolveSandboxSessionTarget(request.Context(), request.PathValue("id"), mode == "desktop")
	if errors.Is(err, errSandboxDesktopNotEnabled) {
		s.writeError(w, http.StatusConflict, "此沙箱未预配图形桌面，请先重启并应用配置或重新创建沙箱")
		return
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	if !s.sessions.hasWorker(target.ServerID) {
		s.writeError(w, http.StatusServiceUnavailable, "沙箱会话服务尚未连接，请先升级并重启目标 Worker")
		return
	}
	token, expiresAt, err := s.sessions.issueTicket(target, mode)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategorySandbox, Action: mode + "-ticket",
		Message:      "签发沙箱" + mode + "会话票据",
		ResourceKind: string(platform.KindSandbox), ResourceID: target.SandboxID,
		Detail: map[string]any{"serverId": target.ServerID, "driver": target.Driver, "mode": mode},
	})
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"ticket": token, "expiresAt": expiresAt,
	})
}

func (s *Server) resolveSandboxSessionTarget(ctx context.Context, sandboxID string, requireDesktop bool) (sandboxSessionTarget, error) {
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
	desktop, _ := sandbox.Spec["desktop"].(bool)
	if driver == "" {
		runtimeID, _ := sandbox.Spec["runtimeId"].(string)
		for index := range resources {
			if resources[index].Kind == platform.KindRuntime && resources[index].ID == runtimeID {
				driver, _ = resources[index].Spec["driver"].(string)
				break
			}
		}
	}
	if requireDesktop && !desktop {
		return sandboxSessionTarget{}, errSandboxDesktopNotEnabled
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
	if !s.negotiateWorkerProtocol(w, request) {
		return
	}
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
	worker := &workerSessionConnection{serverID: serverID, socket: &sessionSocket{conn: conn}}
	if previous := s.sessions.registerWorker(worker); previous != nil {
		_ = previous.socket.conn.Close(websocket.StatusServiceRestart, "worker session replaced")
	}
	s.logger.Info("worker session connected", "server_id", serverID)
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategorySession, Action: "worker-connect",
		Message:      "Worker 会话已建立",
		ResourceKind: "server", ResourceID: serverID,
	})
	connectedAt := time.Now()
	keepaliveDone := make(chan struct{})
	defer func() {
		close(keepaliveDone)
		for _, session := range s.sessions.unregisterWorker(worker) {
			if session.mode != "desktop" {
				_ = session.socket.writeJSON(sessionMessage{Type: "error", Error: "Worker 会话已断开，正在等待重连"})
			}
			_ = session.socket.conn.Close(websocket.StatusTryAgainLater, "worker disconnected")
		}
		worker.socket.conn.CloseNow()
		s.logger.Info("worker session disconnected", "server_id", serverID)
		s.recordLog(request, platform.LogEntry{
			Category: platform.LogCategorySession, Action: "worker-close",
			Message:      "Worker 会话已断开",
			ResourceKind: "server", ResourceID: serverID,
			DurationMS: time.Since(connectedAt).Milliseconds(),
		})
	}()
	go func() {
		ticker := time.NewTicker(sandboxWorkerKeepalive)
		defer ticker.Stop()
		for {
			select {
			case <-keepaliveDone:
				return
			case <-ticker.C:
				if err := worker.socket.writeJSON(sessionMessage{Type: "keepalive"}); err != nil {
					worker.socket.conn.CloseNow()
					return
				}
			}
		}
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
		if browser.mode == "desktop" {
			switch message.Type {
			case "desktop-data":
				data, decodeErr := base64.StdEncoding.DecodeString(message.Data)
				if decodeErr != nil || len(data) > sandboxSessionReadLimit {
					_ = browser.socket.conn.Close(websocket.StatusUnsupportedData, "invalid desktop frame")
					continue
				}
				if err := browser.socket.writeBinary(data); err != nil {
					_ = browser.socket.conn.Close(websocket.StatusInternalError, "browser write failed")
				}
			case "error":
				_ = browser.socket.conn.Close(websocket.StatusInternalError, closeReason(message.Error))
			case "closed":
				_ = browser.socket.conn.Close(websocket.StatusNormalClosure, "desktop session closed")
			}
			continue
		}
		if err := browser.socket.write(websocket.MessageText, payload); err != nil {
			_ = browser.socket.conn.Close(websocket.StatusInternalError, "browser write failed")
		}
	}
}

func (s *Server) connectSandboxSession(w http.ResponseWriter, request *http.Request) {
	ticket, ok := s.sessions.consumeTicket(request.URL.Query().Get("ticket"), request.PathValue("id"), "terminal")
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
	browser := &browserSessionConnection{
		id: uuid.NewString(), serverID: ticket.Target.ServerID, mode: "terminal", socket: &sessionSocket{conn: conn},
	}
	worker, ok := s.sessions.registerBrowser(browser)
	if !ok {
		_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategorySession, Action: "connect",
		Message:      "沙箱会话已建立",
		ResourceKind: string(platform.KindSandbox), ResourceID: ticket.Target.SandboxID,
		Detail: map[string]any{"serverId": ticket.Target.ServerID, "driver": ticket.Target.Driver},
	})
	connectedAt := time.Now()
	defer func() {
		if currentWorker := s.sessions.unregisterBrowser(browser); currentWorker != nil {
			_ = currentWorker.socket.writeJSON(sessionMessage{Type: "close", SessionID: browser.id})
		}
		conn.CloseNow()
		s.recordLog(request, platform.LogEntry{
			Category: platform.LogCategorySession, Action: "close",
			Message:      "沙箱会话已关闭",
			ResourceKind: string(platform.KindSandbox), ResourceID: ticket.Target.SandboxID,
			DurationMS: time.Since(connectedAt).Milliseconds(),
			Detail:     map[string]any{"serverId": ticket.Target.ServerID, "driver": ticket.Target.Driver},
		})
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

func (s *Server) connectSandboxDesktop(w http.ResponseWriter, request *http.Request) {
	ticket, ok := s.sessions.consumeTicket(request.URL.Query().Get("ticket"), request.PathValue("id"), "desktop")
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "沙箱桌面票据无效或已过期")
		return
	}
	conn, err := websocket.Accept(w, request, s.sessions.acceptOptions(request))
	if err != nil {
		s.logger.Warn("accept browser desktop websocket", "sandbox_id", ticket.Target.SandboxID, "error", err)
		return
	}
	conn.SetReadLimit(sandboxSessionReadLimit)
	browser := &browserSessionConnection{
		id: uuid.NewString(), serverID: ticket.Target.ServerID, mode: "desktop", socket: &sessionSocket{conn: conn},
	}
	worker, ok := s.sessions.registerBrowser(browser)
	if !ok {
		_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategorySession, Action: "desktop-connect",
		Message:      "沙箱桌面会话已建立",
		ResourceKind: string(platform.KindSandbox), ResourceID: ticket.Target.SandboxID,
		Detail: map[string]any{"serverId": ticket.Target.ServerID, "driver": ticket.Target.Driver},
	})
	connectedAt := time.Now()
	defer func() {
		if currentWorker := s.sessions.unregisterBrowser(browser); currentWorker != nil {
			_ = currentWorker.socket.writeJSON(sessionMessage{Type: "close", SessionID: browser.id})
		}
		conn.CloseNow()
		s.recordLog(request, platform.LogEntry{
			Category: platform.LogCategorySession, Action: "desktop-close",
			Message:      "沙箱桌面会话已关闭",
			ResourceKind: string(platform.KindSandbox), ResourceID: ticket.Target.SandboxID,
			DurationMS: time.Since(connectedAt).Milliseconds(),
			Detail:     map[string]any{"serverId": ticket.Target.ServerID, "driver": ticket.Target.Driver},
		})
	}()
	if err := worker.socket.writeJSON(sessionMessage{
		Type: "open", Mode: "desktop", SessionID: browser.id, SandboxID: ticket.Target.SandboxID,
		ExternalID: ticket.Target.ExternalID, Driver: ticket.Target.Driver,
	}); err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
		return
	}

	for {
		_, payload, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		if len(payload) == 0 || len(payload) > sandboxSessionReadLimit {
			_ = conn.Close(websocket.StatusMessageTooBig, "desktop frame is too large")
			return
		}
		if err := worker.socket.writeJSON(sessionMessage{
			Type: "desktop-input", SessionID: browser.id, Data: base64.StdEncoding.EncodeToString(payload),
		}); err != nil {
			_ = conn.Close(websocket.StatusTryAgainLater, "worker session unavailable")
			return
		}
	}
}

func closeReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "desktop session failed"
	}
	if len(value) > 120 {
		return value[:120]
	}
	return value
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
