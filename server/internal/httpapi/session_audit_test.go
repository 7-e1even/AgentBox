package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"github.com/coder/websocket"
)

type auditCaptureSessionStore struct {
	sessionTestStore
	mu      sync.Mutex
	entries []platform.LogEntry
	notify  chan platform.LogEntry
	err     error
}

type firstAuditWriteBlocksStore struct {
	sessionTestStore
	mu       sync.Mutex
	attempts []string
	entries  []platform.LogEntry
}

func (store *firstAuditWriteBlocksStore) RecordDurableAudit(ctx context.Context, entry platform.LogEntry) error {
	store.mu.Lock()
	store.attempts = append(store.attempts, entry.Action)
	first := len(store.attempts) == 1
	store.mu.Unlock()
	if first {
		<-ctx.Done()
		return ctx.Err()
	}
	store.mu.Lock()
	store.entries = append(store.entries, entry)
	store.mu.Unlock()
	return nil
}

func (store *auditCaptureSessionStore) RecordDurableAudit(_ context.Context, entry platform.LogEntry) error {
	if store.err != nil {
		return store.err
	}
	store.mu.Lock()
	store.entries = append(store.entries, entry)
	store.mu.Unlock()
	if store.notify != nil {
		store.notify <- entry
	}
	return nil
}

func newAuditSessionServer(t *testing.T, store *auditCaptureSessionStore) (*httptest.Server, *Server) {
	t.Helper()
	t.Setenv("AGENTBOX_TRUSTED_PROXY", "true")
	t.Setenv("AGENTBOX_TRUSTED_PROXY_CIDRS", "127.0.0.0/8")
	handler := New(store, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{DisableAuth: true})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, handler
}

func connectAuditTestWorker(t *testing.T, ctx context.Context, server *httptest.Server, handler *Server) *websocket.Conn {
	t.Helper()
	worker, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/servers/"+sessionTestServerID+"/sessions/connect"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + strings.Repeat("w", 32)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.CloseNow() })
	for !handler.sessions.hasWorker(sessionTestServerID) {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Worker session registration: %v", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	return worker
}

func issueAuditTestTicket(t *testing.T, ctx context.Context, server *httptest.Server) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/sandboxes/sandbox-one/session-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || body.Ticket == "" {
		t.Fatalf("ticket status=%d body=%#v", response.StatusCode, body)
	}
	return body.Ticket
}

func connectAuditTestBrowser(t *testing.T, ctx context.Context, server *httptest.Server, ticket string) *websocket.Conn {
	t.Helper()
	browser, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/session?ticket="+ticket), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"http://agentbox.example:3000"}, "X-Forwarded-Host": []string{"agentbox.example:3000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return browser
}

func TestPrivilegedSessionAuditCarriesIdentityAndOnlySummaryMetadata(t *testing.T) {
	store := &auditCaptureSessionStore{notify: make(chan platform.LogEntry, 3)}
	server, handler := newAuditSessionServer(t, store)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	worker := connectAuditTestWorker(t, ctx, server, handler)
	ticket := issueAuditTestTicket(t, ctx, server)
	browser := connectAuditTestBrowser(t, ctx, server, ticket)

	_, openPayload, err := worker.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var open sessionMessage
	if err := json.Unmarshal(openPayload, &open); err != nil {
		t.Fatal(err)
	}
	secretPath := "/workspace/do-not-audit-this-path"
	secretInput := "do-not-audit-this-terminal-input"
	payload, _ := json.Marshal(sessionMessage{
		Type: "rpc", RequestID: "audit-read", Operation: "read", Path: secretPath, Data: secretInput,
	})
	if err := browser.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	if _, _, err := worker.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if err := browser.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatal(err)
	}

	entries := make([]platform.LogEntry, 0, 3)
	for range 3 {
		select {
		case entry := <-store.notify:
			entries = append(entries, entry)
		case <-ctx.Done():
			t.Fatal("timed out waiting for durable session audit")
		}
	}
	if entries[0].Action != "connect" || entries[1].Action != "activity-summary" || entries[2].Action != "close" {
		t.Fatalf("audit actions=%q,%q,%q", entries[0].Action, entries[1].Action, entries[2].Action)
	}
	sessionID, _ := entries[0].Detail["sessionId"].(string)
	if sessionID == "" || sessionID != open.SessionID {
		t.Fatalf("audit sessionId=%q open sessionId=%q", sessionID, open.SessionID)
	}
	for _, entry := range entries {
		if entry.ActorID != testAdmin().ID || entry.ActorName != testAdmin().Name {
			t.Fatalf("audit actor=%q/%q", entry.ActorID, entry.ActorName)
		}
		if entry.Detail["sessionId"] != sessionID || entry.Detail["channel"] != "terminal" ||
			entry.Detail["sandboxId"] != "sandbox-one" || entry.Detail["serverId"] != sessionTestServerID ||
			entry.Detail["driver"] != "boxlite" {
			t.Fatalf("audit metadata=%#v", entry.Detail)
		}
	}
	if got := entries[1].Detail["rpcReadCount"]; got != uint64(1) {
		t.Fatalf("rpcReadCount=%#v", got)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{secretPath, secretInput, ticket, "login-one"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("durable audit leaked %q: %s", secret, encoded)
		}
	}
	assertSessionAdmissionReleased(t, handler.sessions)
}

func TestPrivilegedSessionFailsClosedBeforeWorkerOpenWhenAuditFails(t *testing.T) {
	store := &auditCaptureSessionStore{err: errors.New("audit unavailable")}
	server, handler := newAuditSessionServer(t, store)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	worker := connectAuditTestWorker(t, ctx, server, handler)
	ticket := issueAuditTestTicket(t, ctx, server)
	browser := connectAuditTestBrowser(t, ctx, server, ticket)
	defer browser.CloseNow()
	if _, _, err := browser.Read(ctx); err == nil {
		t.Fatal("browser remained open after durable connect audit failed")
	}
	readCtx, readCancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer readCancel()
	if _, payload, err := worker.Read(readCtx); err == nil {
		t.Fatalf("Worker received session open despite audit failure: %s", payload)
	}
	assertSessionAdmissionReleased(t, handler.sessions)
}

func TestSessionEndAuditsRetryBlockedFirstWriteAndStillRecordClose(t *testing.T) {
	store := &firstAuditWriteBlocksStore{}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	browser := &browserSessionConnection{
		id: "audit-session", ticket: sandboxSessionTicket{
			AuditSessionID: "audit-session", Mode: "terminal",
			Actor:  platform.AuditActor{Type: "user", ID: "user-one", Name: "User One"},
			Target: sandboxSessionTarget{SandboxID: "sandbox-one", ServerID: "server-one", Driver: "boxlite"},
		},
	}
	server.recordSessionEndAuditsWithTimeout(browser, time.Now().Add(-time.Second), 10*time.Millisecond)

	store.mu.Lock()
	defer store.mu.Unlock()
	if got := strings.Join(store.attempts, ","); got != "activity-summary,activity-summary,close" {
		t.Fatalf("audit attempts=%q", got)
	}
	if len(store.entries) != 2 || store.entries[0].Action != "activity-summary" || store.entries[1].Action != "close" {
		t.Fatalf("durable audit entries=%#v", store.entries)
	}
}
