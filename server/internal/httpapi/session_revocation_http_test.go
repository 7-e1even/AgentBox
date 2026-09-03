package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"github.com/coder/websocket"
)

func newAuthenticatedSessionTestServer(t *testing.T, store PlatformStore) (*httptest.Server, *Server) {
	t.Helper()
	t.Setenv("AGENTBOX_TRUSTED_PROXY", "")
	handler := New(store, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, handler
}

func issueAuthenticatedTicket(t *testing.T, ctx context.Context, server *httptest.Server, cookie string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/sandboxes/sandbox-one/session-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
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

func connectAuthenticatedBrowser(t *testing.T, ctx context.Context, server *httptest.Server, ticket string) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/session?ticket="+ticket), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func TestLogoutClosesCurrentBrowserSessionAndKeepsWorker(t *testing.T) {
	server, handler := newAuthenticatedSessionTestServer(t, sessionTestStore{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	worker := connectAuditTestWorker(t, ctx, server)
	ticket := issueAuthenticatedTicket(t, ctx, server, "login-one")
	browser := connectAuthenticatedBrowser(t, ctx, server, ticket)
	if _, _, err := worker.Read(ctx); err != nil {
		t.Fatal(err)
	}

	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "login-one"})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", response.StatusCode)
	}
	if _, _, err := browser.Read(ctx); err == nil {
		t.Fatal("browser session remained open after logout")
	}
	if !handler.sessions.hasWorker(sessionTestServerID) {
		t.Fatal("logout disconnected the Worker")
	}
}

func TestRejectedBrowserHandshakeReleasesPendingClaim(t *testing.T) {
	server, handler := newAuthenticatedSessionTestServer(t, sessionTestStore{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	connectAuditTestWorker(t, ctx, server)
	ticket := issueAuthenticatedTicket(t, ctx, server, "login-one")

	connection, response, err := websocket.Dial(
		ctx,
		websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/session?ticket="+ticket),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://evil.agentbox.example"}}},
	)
	if connection != nil {
		connection.CloseNow()
		t.Fatal("cross-origin browser handshake unexpectedly succeeded")
	}
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("cross-origin browser handshake did not fail")
	}
	if count := pendingClaimCount(handler.sessions); count != 0 {
		t.Fatalf("pending claims after rejected handshake = %d", count)
	}
	assertSessionAdmissionReleased(t, handler.sessions)
}

type selfPasswordSessionStore struct{ sessionTestStore }

func (selfPasswordSessionStore) UpdateUserPreservingSession(_ context.Context, id string, input platform.UserInput, _ []byte) (platform.User, error) {
	user := testAdmin()
	user.ID, user.Name, user.Username, user.Email = id, input.Name, input.Username, input.Email
	return user, nil
}

func TestSelfPasswordChangeClosesOldChannelButKeepsHTTPLoginUsable(t *testing.T) {
	server, _ := newAuthenticatedSessionTestServer(t, selfPasswordSessionStore{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	worker := connectAuditTestWorker(t, ctx, server)
	ticket := issueAuthenticatedTicket(t, ctx, server, "login-one")
	browser := connectAuthenticatedBrowser(t, ctx, server, ticket)
	if _, _, err := worker.Read(ctx); err != nil {
		t.Fatal(err)
	}

	body := `{"name":"Admin","username":"admin","email":"admin@agentbox.local","password":"new-password123"}`
	request, _ := http.NewRequestWithContext(ctx, http.MethodPatch, server.URL+"/api/auth/me", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "login-one"})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("password update status=%d", response.StatusCode)
	}
	if _, _, err := browser.Read(ctx); err == nil {
		t.Fatal("old browser channel remained open after password change")
	}
	if fresh := issueAuthenticatedTicket(t, ctx, server, "login-one"); fresh == "" {
		t.Fatal("preserved HTTP login could not issue a fresh ticket")
	}
}
