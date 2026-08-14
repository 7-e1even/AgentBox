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

	"agentbox/internal/agent"
	"agentbox/internal/platform"
	"github.com/coder/websocket"
)

const sessionTestServerID = "7b20f83b-6418-4a9f-8477-3dc7c35d6310"

type sessionTestStore struct{ fakeStore }

func (sessionTestStore) ListResources(context.Context) ([]platform.Resource, error) {
	projectID := "default"
	return []platform.Resource{
		{Input: platform.Input{ID: "runtime-one", Kind: platform.KindRuntime, ProjectID: &projectID, Spec: map[string]any{"driver": "docker"}}},
		{Input: platform.Input{ID: "sandbox-one", Kind: platform.KindSandbox, ProjectID: &projectID, Spec: map[string]any{
			"runtimeId": "runtime-one", "serverId": sessionTestServerID,
			"externalId": "agentbox-sandbox-one", "status": "running",
		}}},
	}, nil
}

func newSessionTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	handler := New(
		sessionTestStore{}, agent.BuiltinCatalog,
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		Config{DisableAuth: true},
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func websocketTestURL(serverURL, path string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + path
}

func TestSandboxSessionTicketIsSingleUse(t *testing.T) {
	hub := newSessionHub(nil)
	target := sandboxSessionTarget{SandboxID: "sandbox-one", ServerID: sessionTestServerID, ExternalID: "container", Driver: "docker"}
	token, _, err := hub.issueTicket(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.consumeTicket(token, target.SandboxID); !ok {
		t.Fatal("fresh session ticket was rejected")
	}
	if _, ok := hub.consumeTicket(token, target.SandboxID); ok {
		t.Fatal("session ticket could be consumed twice")
	}
}

func TestSandboxSessionForwardsTerminalFrames(t *testing.T) {
	server := newSessionTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	worker, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/servers/"+sessionTestServerID+"/sessions/connect"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + strings.Repeat("w", 32)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.CloseNow()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/sandboxes/sandbox-one/session-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var ticketBody struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&ticketBody); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || ticketBody.Ticket == "" {
		t.Fatalf("ticket status = %d, ticket = %q", response.StatusCode, ticketBody.Ticket)
	}

	browser, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/session?ticket="+ticketBody.Ticket), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()

	_, openPayload, err := worker.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var open sessionMessage
	if err := json.Unmarshal(openPayload, &open); err != nil {
		t.Fatal(err)
	}
	if open.Type != "open" || open.ExternalID != "agentbox-sandbox-one" || open.SessionID == "" {
		t.Fatalf("unexpected open message: %+v", open)
	}

	if err := browser.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"pwd\r"}`)); err != nil {
		t.Fatal(err)
	}
	_, inputPayload, err := worker.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var input sessionMessage
	if err := json.Unmarshal(inputPayload, &input); err != nil {
		t.Fatal(err)
	}
	if input.Type != "input" || input.Data != "pwd\r" || input.SessionID != open.SessionID {
		t.Fatalf("unexpected input message: %+v", input)
	}

	output, _ := json.Marshal(sessionMessage{Type: "output", SessionID: open.SessionID, Data: "/root\r\n"})
	if err := worker.Write(ctx, websocket.MessageText, output); err != nil {
		t.Fatal(err)
	}
	_, outputPayload, err := browser.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outputPayload), `/root\r\n`) {
		t.Fatalf("unexpected browser output: %s", outputPayload)
	}
}
