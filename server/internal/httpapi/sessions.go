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
	"sync/atomic"
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
	sandboxSessionMaxLifetime    = 8 * time.Hour
	sandboxSessionAuditTimeout   = 5 * time.Second
	sandboxSessionAuditAttempts  = 2

	sandboxSessionOutstandingGlobalLimit = 256
	sandboxSessionOutstandingUserLimit   = 32
	sandboxSessionOutstandingLoginLimit  = 16
	sandboxSessionIssuingGlobalLimit     = 8
	sandboxSessionIssuingLoginLimit      = 4
	sandboxSessionIssueGlobalRateLimit   = 600
	sandboxSessionIssueUserRateLimit     = 120
	sandboxSessionIssueLoginRateLimit    = 60
	sandboxSessionIssueRateWindow        = time.Minute
)

var (
	errSandboxDesktopNotEnabled      = errors.New("sandbox desktop is not enabled")
	errSandboxSessionCapacityLimited = errors.New("sandbox session capacity is exhausted")
	errSandboxSessionRateLimited     = errors.New("sandbox session ticket rate is exceeded")
)

type sandboxSessionTarget struct {
	SandboxID  string
	ServerID   string
	ExternalID string
	Driver     string
}

type sandboxSessionTicket struct {
	Target           sandboxSessionTarget
	Mode             string
	ExpiresAt        time.Time
	SessionExpiresAt time.Time
	AuditSessionID   string
	Actor            platform.AuditActor
	UserID           string
	SessionKey       string
	Role             platform.UserRole
	reservation      *sandboxSessionReservation
}

type sandboxSessionState string

const (
	sandboxSessionStateIssuing sandboxSessionState = "issuing"
	sandboxSessionStateTicket  sandboxSessionState = "ticket"
	sandboxSessionStatePending sandboxSessionState = "pending"
	sandboxSessionStateActive  sandboxSessionState = "active"
	sandboxSessionStateDone    sandboxSessionState = "done"
)

type sandboxSessionReservation struct {
	state      sandboxSessionState
	userID     string
	sessionKey string
	sandboxID  string
	token      string
	sessionID  string
}

type sandboxSessionRateHistory struct {
	global  []time.Time
	byUser  map[string][]time.Time
	byLogin map[string][]time.Time
	lastGC  time.Time
}

type sessionAuthorization struct {
	userID     string
	sessionKey string
	role       platform.UserRole
	actor      platform.AuditActor
	epoch      uint64
}

type sessionAuthorizationContextKey struct{}

type sessionActivity struct {
	inputMessages  atomic.Uint64
	inputBytes     atomic.Uint64
	outputMessages atomic.Uint64
	outputBytes    atomic.Uint64
	resizeCount    atomic.Uint64
	rpcListCount   atomic.Uint64
	rpcReadCount   atomic.Uint64
	rpcWriteCount  atomic.Uint64
	rpcUploadCount atomic.Uint64
}

func (activity *sessionActivity) recordInput(message sessionMessage, bytes int) {
	activity.inputMessages.Add(1)
	activity.inputBytes.Add(uint64(bytes))
	switch message.Type {
	case "resize":
		activity.resizeCount.Add(1)
	case "rpc":
		switch message.Operation {
		case "list":
			activity.rpcListCount.Add(1)
		case "read":
			activity.rpcReadCount.Add(1)
		case "write":
			activity.rpcWriteCount.Add(1)
		case "upload-start", "upload-chunk", "upload-finish", "upload-cancel":
			activity.rpcUploadCount.Add(1)
		}
	}
}

func (activity *sessionActivity) recordOutput(bytes int) {
	activity.outputMessages.Add(1)
	activity.outputBytes.Add(uint64(bytes))
}

func (activity *sessionActivity) auditDetail() map[string]any {
	return map[string]any{
		"inputMessages": activity.inputMessages.Load(), "inputBytes": activity.inputBytes.Load(),
		"outputMessages": activity.outputMessages.Load(), "outputBytes": activity.outputBytes.Load(),
		"resizeCount": activity.resizeCount.Load(), "rpcListCount": activity.rpcListCount.Load(),
		"rpcReadCount": activity.rpcReadCount.Load(), "rpcWriteCount": activity.rpcWriteCount.Load(),
		"rpcUploadCount": activity.rpcUploadCount.Load(),
	}
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
	ticket   sandboxSessionTicket
	socket   *sessionSocket
	activity sessionActivity
}

type sessionHub struct {
	// Session state is process-local. Server replicas and cross-instance
	// ticket routing are unsupported; restarting requires fresh connections.
	revocationMu       sync.Mutex
	mu                 sync.RWMutex
	workers            map[string]*workerSessionConnection
	sessions           map[string]*browserSessionConnection
	tickets            map[string]sandboxSessionTicket
	authEpoch          uint64
	reservations       map[*sandboxSessionReservation]struct{}
	pendingClaims      map[*sandboxSessionReservation]struct{}
	outstanding        int
	outstandingByUser  map[string]int
	outstandingByLogin map[string]int
	issuing            int
	issuingByLogin     map[string]int
	issueRate          sandboxSessionRateHistory
	originPatterns     []string
	trustedProxy       trustedProxySettings
}

func newSessionHub(origins []string, trustedProxy trustedProxySettings) *sessionHub {
	patterns := make([]string, 0, len(origins))
	for _, origin := range origins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return &sessionHub{
		workers:            make(map[string]*workerSessionConnection),
		sessions:           make(map[string]*browserSessionConnection),
		tickets:            make(map[string]sandboxSessionTicket),
		reservations:       make(map[*sandboxSessionReservation]struct{}),
		pendingClaims:      make(map[*sandboxSessionReservation]struct{}),
		outstandingByUser:  make(map[string]int),
		outstandingByLogin: make(map[string]int),
		issuingByLogin:     make(map[string]int),
		issueRate: sandboxSessionRateHistory{
			byUser: make(map[string][]time.Time), byLogin: make(map[string][]time.Time),
		},
		originPatterns: patterns,
		trustedProxy:   trustedProxy,
	}
}

func (h *sessionHub) acceptOptions(request *http.Request) *websocket.AcceptOptions {
	patterns := slices.Clone(h.originPatterns)
	// 仅在信任代理时才把 X-Forwarded-Host 并入 Origin 白名单，防止伪造头绕过校验。
	peer, peerOK := requestPeerAddress(request)
	if peerOK && h.trustedProxy.enabled && h.trustedProxy.contains(peer) {
		if forwardedHost := forwardedHostPattern(singleForwardedValue(request.Header.Values("X-Forwarded-Host"))); forwardedHost != "" {
			patterns = append(patterns, forwardedHost)
		}
	}
	return &websocket.AcceptOptions{OriginPatterns: patterns}
}

func singleForwardedValue(lines []string) string {
	var result string
	for _, line := range lines {
		for raw := range strings.SplitSeq(line, ",") {
			value := strings.TrimSpace(raw)
			if value == "" || result != "" {
				return ""
			}
			result = value
		}
	}
	return result
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

func (h *sessionHub) authorizationEpoch() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.authEpoch
}

func pruneSessionRateHistory(history []time.Time, cutoff time.Time) []time.Time {
	kept := history[:0]
	for _, timestamp := range history {
		if timestamp.After(cutoff) {
			kept = append(kept, timestamp)
		}
	}
	return kept
}

func (h *sessionHub) sweepSessionRateHistoryLocked(now time.Time) {
	if !h.issueRate.lastGC.IsZero() && now.Before(h.issueRate.lastGC.Add(sandboxSessionIssueRateWindow)) {
		return
	}
	cutoff := now.Add(-sandboxSessionIssueRateWindow)
	for key, history := range h.issueRate.byUser {
		history = pruneSessionRateHistory(history, cutoff)
		if len(history) == 0 {
			delete(h.issueRate.byUser, key)
		} else {
			h.issueRate.byUser[key] = history
		}
	}
	for key, history := range h.issueRate.byLogin {
		history = pruneSessionRateHistory(history, cutoff)
		if len(history) == 0 {
			delete(h.issueRate.byLogin, key)
		} else {
			h.issueRate.byLogin[key] = history
		}
	}
	h.issueRate.lastGC = now
}

func (h *sessionHub) recordSessionIssueRateLocked(authorization sessionAuthorization, now time.Time) bool {
	h.sweepSessionRateHistoryLocked(now)
	cutoff := now.Add(-sandboxSessionIssueRateWindow)
	h.issueRate.global = pruneSessionRateHistory(h.issueRate.global, cutoff)
	userHistory := pruneSessionRateHistory(h.issueRate.byUser[authorization.userID], cutoff)
	if len(userHistory) == 0 {
		delete(h.issueRate.byUser, authorization.userID)
	} else {
		h.issueRate.byUser[authorization.userID] = userHistory
	}
	loginHistory := pruneSessionRateHistory(h.issueRate.byLogin[authorization.sessionKey], cutoff)
	if len(loginHistory) == 0 {
		delete(h.issueRate.byLogin, authorization.sessionKey)
	} else {
		h.issueRate.byLogin[authorization.sessionKey] = loginHistory
	}
	if len(h.issueRate.global) >= sandboxSessionIssueGlobalRateLimit ||
		len(userHistory) >= sandboxSessionIssueUserRateLimit ||
		len(loginHistory) >= sandboxSessionIssueLoginRateLimit {
		return false
	}

	h.issueRate.global = append(h.issueRate.global, now)
	h.issueRate.byUser[authorization.userID] = append(userHistory, now)
	h.issueRate.byLogin[authorization.sessionKey] = append(loginHistory, now)
	return true
}

func incrementSessionCount(counts map[string]int, key string) {
	counts[key]++
}

func decrementSessionCount(counts map[string]int, key string) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}

func (h *sessionHub) finishReservationLocked(reservation *sandboxSessionReservation) *browserSessionConnection {
	if reservation == nil || reservation.state == sandboxSessionStateDone {
		return nil
	}
	if _, ok := h.reservations[reservation]; !ok {
		reservation.state = sandboxSessionStateDone
		return nil
	}

	var active *browserSessionConnection
	switch reservation.state {
	case sandboxSessionStateIssuing:
		if h.issuing > 0 {
			h.issuing--
		}
		decrementSessionCount(h.issuingByLogin, reservation.sessionKey)
	case sandboxSessionStateTicket:
		if ticket, ok := h.tickets[reservation.token]; ok && ticket.reservation == reservation {
			delete(h.tickets, reservation.token)
		}
	case sandboxSessionStatePending:
		delete(h.pendingClaims, reservation)
	case sandboxSessionStateActive:
		if connection, ok := h.sessions[reservation.sessionID]; ok && connection.ticket.reservation == reservation {
			active = connection
			delete(h.sessions, reservation.sessionID)
		}
	}

	if h.outstanding > 0 {
		h.outstanding--
	}
	decrementSessionCount(h.outstandingByUser, reservation.userID)
	decrementSessionCount(h.outstandingByLogin, reservation.sessionKey)
	delete(h.reservations, reservation)
	reservation.token = ""
	reservation.sessionID = ""
	reservation.state = sandboxSessionStateDone
	return active
}

func (h *sessionHub) expireTicketsLocked(now time.Time) {
	for _, ticket := range h.tickets {
		if !now.Before(ticket.ExpiresAt) {
			h.finishReservationLocked(ticket.reservation)
		}
	}
}

func (h *sessionHub) beginTicketIssue(authorization sessionAuthorization, sandboxID string, now time.Time) (*sandboxSessionReservation, error) {
	if authorization.userID == "" || authorization.sessionKey == "" || sandboxID == "" {
		return nil, store.ErrUnauthorized
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireTicketsLocked(now)
	if !h.recordSessionIssueRateLocked(authorization, now) {
		return nil, errSandboxSessionRateLimited
	}
	if h.outstanding >= sandboxSessionOutstandingGlobalLimit ||
		h.outstandingByUser[authorization.userID] >= sandboxSessionOutstandingUserLimit ||
		h.outstandingByLogin[authorization.sessionKey] >= sandboxSessionOutstandingLoginLimit ||
		h.issuing >= sandboxSessionIssuingGlobalLimit ||
		h.issuingByLogin[authorization.sessionKey] >= sandboxSessionIssuingLoginLimit {
		return nil, errSandboxSessionCapacityLimited
	}

	reservation := &sandboxSessionReservation{
		state: sandboxSessionStateIssuing, userID: authorization.userID,
		sessionKey: authorization.sessionKey, sandboxID: sandboxID,
	}
	h.reservations[reservation] = struct{}{}
	h.outstanding++
	incrementSessionCount(h.outstandingByUser, reservation.userID)
	incrementSessionCount(h.outstandingByLogin, reservation.sessionKey)
	h.issuing++
	incrementSessionCount(h.issuingByLogin, reservation.sessionKey)
	return reservation, nil
}

func (h *sessionHub) releaseTicketIssue(reservation *sandboxSessionReservation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if reservation != nil && reservation.state == sandboxSessionStateIssuing {
		h.finishReservationLocked(reservation)
	}
}

func (h *sessionHub) commitTicket(reservation *sandboxSessionReservation, target sandboxSessionTarget, mode string, authorization sessionAuthorization) (string, time.Time, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, fmt.Errorf("generate sandbox session ticket: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	now := time.Now().UTC()
	expiresAt := now.Add(sandboxSessionTicketLifetime)
	h.mu.Lock()
	defer h.mu.Unlock()
	if reservation == nil || reservation.state != sandboxSessionStateIssuing ||
		authorization.epoch != h.authEpoch || reservation.userID != authorization.userID ||
		reservation.sessionKey != authorization.sessionKey || reservation.sandboxID != target.SandboxID {
		return "", time.Time{}, store.ErrUnauthorized
	}
	if _, exists := h.tickets[token]; exists {
		return "", time.Time{}, errors.New("generated duplicate sandbox session ticket")
	}
	if h.issuing > 0 {
		h.issuing--
	}
	decrementSessionCount(h.issuingByLogin, reservation.sessionKey)
	reservation.state = sandboxSessionStateTicket
	reservation.token = token
	ticket := sandboxSessionTicket{
		Target: target, Mode: mode, ExpiresAt: expiresAt,
		SessionExpiresAt: now.Add(sandboxSessionMaxLifetime), AuditSessionID: uuid.NewString(),
		Actor: authorization.actor, UserID: authorization.userID, SessionKey: authorization.sessionKey,
		Role: authorization.role, reservation: reservation,
	}
	reservation.sessionID = ticket.AuditSessionID
	h.tickets[token] = ticket
	return token, expiresAt, nil
}

func (h *sessionHub) issueTicket(target sandboxSessionTarget, mode string, authorization sessionAuthorization) (string, time.Time, error) {
	reservation, err := h.beginTicketIssue(authorization, target.SandboxID, time.Now().UTC())
	if err != nil {
		return "", time.Time{}, err
	}
	token, expiresAt, err := h.commitTicket(reservation, target, mode, authorization)
	if err != nil {
		h.releaseTicketIssue(reservation)
	}
	return token, expiresAt, err
}

func (h *sessionHub) retryTicketAfterRevalidation(reservation *sandboxSessionReservation, target sandboxSessionTarget, mode string, revalidate func() (sessionAuthorization, error)) (string, time.Time, error) {
	h.revocationMu.Lock()
	defer h.revocationMu.Unlock()
	h.mu.RLock()
	issuing := reservation != nil && reservation.state == sandboxSessionStateIssuing
	h.mu.RUnlock()
	if !issuing {
		return "", time.Time{}, store.ErrUnauthorized
	}
	authorization, err := revalidate()
	if err != nil {
		return "", time.Time{}, err
	}
	authorization.epoch = h.authorizationEpoch()
	return h.commitTicket(reservation, target, mode, authorization)
}

func (h *sessionHub) consumeTicket(token, sandboxID, mode string) (sandboxSessionTicket, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	h.expireTicketsLocked(now)
	ticket, ok := h.tickets[token]
	if !ok {
		return sandboxSessionTicket{}, false
	}
	reservation := ticket.reservation
	if reservation == nil || reservation.state != sandboxSessionStateTicket || reservation.token != token ||
		ticket.Target.SandboxID != sandboxID || ticket.Mode != mode || !now.Before(ticket.ExpiresAt) || !h.authorizationValid(ticket) {
		h.finishReservationLocked(reservation)
		return sandboxSessionTicket{}, false
	}
	delete(h.tickets, token)
	reservation.token = ""
	reservation.state = sandboxSessionStatePending
	h.pendingClaims[reservation] = struct{}{}
	return ticket, true
}

func (h *sessionHub) authorizationValid(ticket sandboxSessionTicket) bool {
	return time.Now().UTC().Before(ticket.SessionExpiresAt)
}

func (h *sessionHub) revokeSession(sessionKey string) []*browserSessionConnection {
	return h.revokeReservations(true, func(reservation *sandboxSessionReservation) bool {
		return reservation.sessionKey == sessionKey
	})
}

func (h *sessionHub) revokeUser(userID string) []*browserSessionConnection {
	return h.revokeReservations(true, func(reservation *sandboxSessionReservation) bool {
		return reservation.userID == userID
	})
}

func (h *sessionHub) revokeSandbox(sandboxID string) []*browserSessionConnection {
	return h.revokeReservations(false, func(reservation *sandboxSessionReservation) bool {
		return reservation.sandboxID == sandboxID
	})
}

func (h *sessionHub) revokeReservations(incrementEpoch bool, matches func(*sandboxSessionReservation) bool) []*browserSessionConnection {
	h.revocationMu.Lock()
	defer h.revocationMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if incrementEpoch {
		h.authEpoch++
	}
	removed := make([]*browserSessionConnection, 0)
	for reservation := range h.reservations {
		if matches(reservation) {
			if active := h.finishReservationLocked(reservation); active != nil {
				removed = append(removed, active)
			}
		}
	}
	return removed
}

func (s *Server) revokeLoginSandboxSessions(sessionKey string) {
	s.closeRevokedSandboxSessions(s.sessions.revokeSession(sessionKey))
}

func (s *Server) revokeUserSandboxSessions(userID string) {
	s.closeRevokedSandboxSessions(s.sessions.revokeUser(userID))
}

func (s *Server) closeRevokedSandboxSessions(connections []*browserSessionConnection) {
	for _, connection := range connections {
		if connection.socket != nil && connection.socket.conn != nil {
			_ = connection.socket.conn.CloseNow()
		}
	}
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
	for _, session := range h.sessions {
		if session.serverID == connection.serverID {
			disconnected = append(disconnected, session)
			h.finishReservationLocked(session.ticket.reservation)
		}
	}
	return disconnected
}

func (h *sessionHub) registerBrowser(connection *browserSessionConnection) (*workerSessionConnection, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reservation := connection.ticket.reservation
	if reservation == nil {
		return nil, false
	}
	if reservation.state != sandboxSessionStatePending {
		return nil, false
	}
	if _, ok := h.pendingClaims[reservation]; !ok {
		h.finishReservationLocked(reservation)
		return nil, false
	}
	if reservation.userID != connection.ticket.UserID || reservation.sessionKey != connection.ticket.SessionKey ||
		reservation.sandboxID != connection.ticket.Target.SandboxID || reservation.sessionID != connection.id ||
		connection.serverID != connection.ticket.Target.ServerID {
		h.finishReservationLocked(reservation)
		return nil, false
	}
	worker := h.workers[connection.serverID]
	if worker == nil || !h.authorizationValid(connection.ticket) || h.sessions[connection.id] != nil {
		h.finishReservationLocked(reservation)
		return nil, false
	}
	delete(h.pendingClaims, reservation)
	reservation.state = sandboxSessionStateActive
	h.sessions[connection.id] = connection
	return worker, true
}

func (h *sessionHub) releaseTicketClaim(ticket sandboxSessionTicket) {
	if ticket.reservation == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ticket.reservation.state == sandboxSessionStatePending {
		h.finishReservationLocked(ticket.reservation)
	}
}

func (h *sessionHub) openBrowser(connection *browserSessionConnection, message sessionMessage) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.sessions[connection.id] != connection || !h.authorizationValid(connection.ticket) {
		return store.ErrUnauthorized
	}
	worker := h.workers[connection.serverID]
	if worker == nil {
		return store.ErrConflict
	}
	return worker.socket.writeJSON(message)
}

func (h *sessionHub) unregisterBrowser(connection *browserSessionConnection) *workerSessionConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[connection.id] == connection {
		h.finishReservationLocked(connection.ticket.reservation)
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
	authorization, ok := sessionAuthorizationFromContext(request.Context())
	user := userFromContext(request.Context())
	if !ok || user.ID == "" || user.ID != authorization.userID {
		s.writeError(w, http.StatusUnauthorized, "登录状态无效或已过期")
		return
	}
	sandboxID := request.PathValue("id")
	reservation, err := s.sessions.beginTicketIssue(authorization, sandboxID, time.Now().UTC())
	if err != nil {
		s.writeSandboxSessionAdmissionError(w, err)
		return
	}
	defer s.sessions.releaseTicketIssue(reservation)

	if err := s.store.AuthorizeSandboxCredentialAccess(request.Context(), sandboxID); err != nil {
		s.handleError(w, err)
		return
	}
	target, err := s.resolveSandboxSessionTarget(request.Context(), sandboxID, mode == "desktop")
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
	token, expiresAt, err := s.issueSandboxSessionTicket(request, reservation, target, mode, authorization)
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

func (s *Server) writeSandboxSessionAdmissionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSandboxSessionRateLimited):
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "沙箱会话票据请求过于频繁，请稍后重试")
	case errors.Is(err, errSandboxSessionCapacityLimited):
		w.Header().Set("Retry-After", "1")
		s.writeError(w, http.StatusTooManyRequests, "沙箱会话容量已满，请稍后重试")
	default:
		s.handleError(w, err)
	}
}

func (s *Server) issueSandboxSessionTicket(request *http.Request, reservation *sandboxSessionReservation, target sandboxSessionTarget, mode string, authorization sessionAuthorization) (string, time.Time, error) {
	token, expiresAt, err := s.sessions.commitTicket(reservation, target, mode, authorization)
	if !errors.Is(err, store.ErrUnauthorized) {
		return token, expiresAt, err
	}
	return s.sessions.retryTicketAfterRevalidation(reservation, target, mode, func() (sessionAuthorization, error) {
		return s.revalidateSessionAuthorization(request, authorization)
	})
}

func (s *Server) revalidateSessionAuthorization(request *http.Request, expected sessionAuthorization) (sessionAuthorization, error) {
	var (
		user platform.User
		key  string
		err  error
	)
	if s.disableAuth {
		user, err = s.debugUser(request.Context())
		key = "debug:" + user.ID
	} else {
		var tokenHash []byte
		var ok bool
		tokenHash, ok = sessionHash(request)
		if !ok {
			return sessionAuthorization{}, store.ErrUnauthorized
		}
		key = sessionKey(tokenHash)
		if key != expected.sessionKey {
			return sessionAuthorization{}, store.ErrUnauthorized
		}
		user, err = s.store.UserBySession(request.Context(), tokenHash)
	}
	if err != nil {
		return sessionAuthorization{}, err
	}
	if user.ID == "" || user.ID != expected.userID || key != expected.sessionKey || user.Role != expected.role || user.Status != platform.UserStatusActive {
		return sessionAuthorization{}, store.ErrUnauthorized
	}
	return sessionAuthorization{
		userID: user.ID, sessionKey: key, role: user.Role,
		actor: platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name, Role: user.Role},
	}, nil
}

type durableSessionAuditStore interface {
	RecordDurableAudit(context.Context, platform.LogEntry) error
}

func (s *Server) recordSessionAudit(ctx context.Context, ticket sandboxSessionTicket, phase string, duration time.Duration, extra map[string]any) error {
	auditor, ok := s.store.(durableSessionAuditStore)
	if !ok {
		return errors.New("durable session audit store is unavailable")
	}
	detail := map[string]any{
		"sessionId": ticket.AuditSessionID, "channel": ticket.Mode,
		"sandboxId": ticket.Target.SandboxID, "serverId": ticket.Target.ServerID,
		"driver": ticket.Target.Driver, "role": string(ticket.Role),
	}
	for key, value := range extra {
		detail[key] = value
	}
	action := phase
	if ticket.Mode == "desktop" {
		action = "desktop-" + phase
	}
	entry := platform.LogEntry{
		Category: platform.LogCategorySession, Action: action,
		ActorID: ticket.Actor.ID, ActorName: ticket.Actor.Name,
		ResourceKind: string(platform.KindSandbox), ResourceID: ticket.Target.SandboxID,
		DurationMS: duration.Milliseconds(), Detail: detail,
	}
	return auditor.RecordDurableAudit(platform.WithAuditActor(ctx, ticket.Actor), entry)
}

func (s *Server) recordSessionEndAudits(browser *browserSessionConnection, connectedAt time.Time) {
	s.recordSessionEndAuditsWithTimeout(browser, connectedAt, sandboxSessionAuditTimeout)
}

func (s *Server) recordSessionEndAuditsWithTimeout(browser *browserSessionConnection, connectedAt time.Time, attemptTimeout time.Duration) {
	if err := s.recordSessionAuditWithRetry(browser.ticket, "activity-summary", time.Since(connectedAt), browser.activity.auditDetail(), attemptTimeout); err != nil {
		s.logger.Error("record sandbox session activity audit", "session_id", browser.id, "error", err)
	}
	if err := s.recordSessionAuditWithRetry(browser.ticket, "close", time.Since(connectedAt), nil, attemptTimeout); err != nil {
		s.logger.Error("record sandbox session close audit", "session_id", browser.id, "error", err)
	}
}

func (s *Server) recordSessionAuditWithRetry(ticket sandboxSessionTicket, phase string, duration time.Duration, extra map[string]any, attemptTimeout time.Duration) error {
	var lastErr error
	for attempt := range sandboxSessionAuditAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		lastErr = s.recordSessionAudit(ctx, ticket, phase, duration, extra)
		cancel()
		if lastErr == nil {
			return nil
		}
		if attempt+1 < sandboxSessionAuditAttempts {
			s.logger.Warn("retry durable sandbox session audit", "session_id", ticket.AuditSessionID, "phase", phase, "error", lastErr)
		}
	}
	return fmt.Errorf("record durable session %s audit after %d attempts: %w", phase, sandboxSessionAuditAttempts, lastErr)
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
				browser.activity.recordOutput(len(data))
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
		browser.activity.recordOutput(len(payload))
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
	claimPending := true
	defer func() {
		if claimPending {
			s.sessions.releaseTicketClaim(ticket)
		}
	}()
	conn, err := websocket.Accept(w, request, s.sessions.acceptOptions(request))
	if err != nil {
		s.logger.Warn("accept browser session websocket", "sandbox_id", ticket.Target.SandboxID, "error", err)
		return
	}
	conn.SetReadLimit(sandboxSessionReadLimit)
	browser := &browserSessionConnection{
		id: ticket.AuditSessionID, serverID: ticket.Target.ServerID, mode: "terminal",
		ticket: ticket, socket: &sessionSocket{conn: conn},
	}
	_, ok = s.sessions.registerBrowser(browser)
	claimPending = false
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
		return
	}
	connectedAt := time.Now()
	if err := s.recordSessionAudit(request.Context(), ticket, "connect", 0, nil); err != nil {
		s.sessions.unregisterBrowser(browser)
		_ = conn.Close(websocket.StatusInternalError, "session audit unavailable")
		s.logger.Error("record sandbox session connect audit", "session_id", browser.id, "error", err)
		return
	}
	opened := false
	defer func() {
		if currentWorker := s.sessions.unregisterBrowser(browser); currentWorker != nil {
			if opened {
				_ = currentWorker.socket.writeJSON(sessionMessage{Type: "close", SessionID: browser.id})
			}
		}
		conn.CloseNow()
		s.recordSessionEndAudits(browser, connectedAt)
	}()
	if err := s.sessions.openBrowser(browser, sessionMessage{
		Type: "open", SessionID: browser.id, SandboxID: ticket.Target.SandboxID,
		ExternalID: ticket.Target.ExternalID, Driver: ticket.Target.Driver, Cols: 120, Rows: 30,
	}); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
		return
	}
	opened = true

	readCtx, cancel := context.WithDeadline(context.Background(), ticket.SessionExpiresAt)
	defer cancel()
	for {
		_, payload, err := conn.Read(readCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				_ = conn.Close(websocket.StatusPolicyViolation, "session lifetime exceeded")
			}
			return
		}
		message, ok := validBrowserSessionMessage(payload)
		if !ok {
			_ = browser.socket.writeJSON(sessionMessage{Type: "error", Error: "无效的会话消息"})
			continue
		}
		browser.activity.recordInput(message, len(payload))
		message.SessionID = browser.id
		if err := s.sessions.openBrowser(browser, message); err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
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
	claimPending := true
	defer func() {
		if claimPending {
			s.sessions.releaseTicketClaim(ticket)
		}
	}()
	conn, err := websocket.Accept(w, request, s.sessions.acceptOptions(request))
	if err != nil {
		s.logger.Warn("accept browser desktop websocket", "sandbox_id", ticket.Target.SandboxID, "error", err)
		return
	}
	conn.SetReadLimit(sandboxSessionReadLimit)
	browser := &browserSessionConnection{
		id: ticket.AuditSessionID, serverID: ticket.Target.ServerID, mode: "desktop",
		ticket: ticket, socket: &sessionSocket{conn: conn},
	}
	_, ok = s.sessions.registerBrowser(browser)
	claimPending = false
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
		return
	}
	connectedAt := time.Now()
	if err := s.recordSessionAudit(request.Context(), ticket, "connect", 0, nil); err != nil {
		s.sessions.unregisterBrowser(browser)
		_ = conn.Close(websocket.StatusInternalError, "session audit unavailable")
		s.logger.Error("record sandbox desktop connect audit", "session_id", browser.id, "error", err)
		return
	}
	opened := false
	defer func() {
		if currentWorker := s.sessions.unregisterBrowser(browser); currentWorker != nil {
			if opened {
				_ = currentWorker.socket.writeJSON(sessionMessage{Type: "close", SessionID: browser.id})
			}
		}
		conn.CloseNow()
		s.recordSessionEndAudits(browser, connectedAt)
	}()
	if err := s.sessions.openBrowser(browser, sessionMessage{
		Type: "open", Mode: "desktop", SessionID: browser.id, SandboxID: ticket.Target.SandboxID,
		ExternalID: ticket.Target.ExternalID, Driver: ticket.Target.Driver,
	}); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
		return
	}
	opened = true

	readCtx, cancel := context.WithDeadline(context.Background(), ticket.SessionExpiresAt)
	defer cancel()
	for {
		_, payload, err := conn.Read(readCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				_ = conn.Close(websocket.StatusPolicyViolation, "session lifetime exceeded")
			}
			return
		}
		if len(payload) == 0 || len(payload) > sandboxSessionReadLimit {
			_ = conn.Close(websocket.StatusMessageTooBig, "desktop frame is too large")
			return
		}
		browser.activity.recordInput(sessionMessage{Type: "desktop-input"}, len(payload))
		if err := s.sessions.openBrowser(browser, sessionMessage{
			Type: "desktop-input", SessionID: browser.id, Data: base64.StdEncoding.EncodeToString(payload),
		}); err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "authorization changed or worker unavailable")
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
