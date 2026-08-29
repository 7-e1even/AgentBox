package httpapi

import (
	"context"
	"encoding/base64"
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

const sessionTestServerID = "7b20f83b-6418-4a9f-8477-3dc7c35d6310"

type sessionTestStore struct{ fakeStore }

func (sessionTestStore) ListResources(context.Context) ([]platform.Resource, error) {
	projectID := "default"
	return []platform.Resource{
		{Input: platform.Input{ID: "runtime-one", Kind: platform.KindRuntime, ProjectID: &projectID, Spec: map[string]any{"driver": "docker", "desktop": true}}},
		{Input: platform.Input{ID: "sandbox-one", Kind: platform.KindSandbox, ProjectID: &projectID, Spec: map[string]any{
			"runtimeId": "runtime-one", "serverId": sessionTestServerID,
			"externalId": "agentbox-sandbox-one", "status": "running", "driver": "boxlite",
		}}},
	}, nil
}

func newSessionTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	// 该测试模拟反向代理部署（依赖 X-Forwarded-Host 放行浏览器 Origin），
	// 因此显式开启可信代理模式。
	t.Setenv("AGENTBOX_TRUSTED_PROXY", "true")
	handler := New(
		sessionTestStore{}, catalog.BuiltinCatalog,
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
	hub := newSessionHub(nil, false)
	target := sandboxSessionTarget{SandboxID: "sandbox-one", ServerID: sessionTestServerID, ExternalID: "container", Driver: "docker"}
	token, _, err := hub.issueTicket(target, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.consumeTicket(token, target.SandboxID, "terminal"); !ok {
		t.Fatal("fresh session ticket was rejected")
	}
	if _, ok := hub.consumeTicket(token, target.SandboxID, "terminal"); ok {
		t.Fatal("session ticket could be consumed twice")
	}
}

func TestSandboxSessionTicketCannotChangeMode(t *testing.T) {
	hub := newSessionHub(nil, false)
	target := sandboxSessionTarget{SandboxID: "sandbox-one", ServerID: sessionTestServerID, ExternalID: "container", Driver: "docker"}
	token, _, err := hub.issueTicket(target, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.consumeTicket(token, target.SandboxID, "terminal"); ok {
		t.Fatal("desktop ticket was accepted by the terminal endpoint")
	}
}

func TestSessionAcceptOptionsAllowForwardedGatewayHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://server:8091/api/sessions/connect", nil)
	request.Header.Set("X-Forwarded-Host", "agentbox.example:3000")
	patterns := newSessionHub(nil, true).acceptOptions(request).OriginPatterns
	if len(patterns) != 1 || patterns[0] != "agentbox.example:3000" {
		t.Fatalf("origin patterns = %#v", patterns)
	}
}

func TestSessionAcceptOptionsRejectInvalidForwardedGatewayHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://server:8091/api/sessions/connect", nil)
	request.Header.Set("X-Forwarded-Host", "*")
	if patterns := newSessionHub(nil, true).acceptOptions(request).OriginPatterns; len(patterns) != 0 {
		t.Fatalf("origin patterns = %#v", patterns)
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

	browser, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/session?ticket="+ticketBody.Ticket), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":           []string{"http://agentbox.example:3000"},
			"X-Forwarded-Host": []string{"agentbox.example:3000"},
		},
	})
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
	if open.Type != "open" || open.ExternalID != "agentbox-sandbox-one" || open.Driver != "boxlite" || open.SessionID == "" {
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

	closed, _ := json.Marshal(sessionMessage{Type: "closed", SessionID: open.SessionID, Retryable: true})
	if err := worker.Write(ctx, websocket.MessageText, closed); err != nil {
		t.Fatal(err)
	}
	_, closedPayload, err := browser.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(closedPayload), `"retryable":true`) {
		t.Fatalf("unexpected browser close message: %s", closedPayload)
	}

	uploadID := "3e8774b5-4921-4c8c-8927-2b7a30d518d2"
	uploadRequest, _ := json.Marshal(sessionMessage{
		Type: "rpc", RequestID: "upload-one", Operation: "upload-chunk",
		Path: "/workspace/example.bin", Content: "YWdlbnRib3g=", UploadID: uploadID,
	})
	if err := browser.Write(ctx, websocket.MessageText, uploadRequest); err != nil {
		t.Fatal(err)
	}
	_, forwardedPayload, err := worker.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var forwarded sessionMessage
	if err := json.Unmarshal(forwardedPayload, &forwarded); err != nil {
		t.Fatal(err)
	}
	if forwarded.SessionID != open.SessionID || forwarded.Operation != "upload-chunk" || forwarded.UploadID != uploadID || forwarded.Content != "YWdlbnRib3g=" {
		t.Fatalf("unexpected forwarded upload: %+v", forwarded)
	}

	uploadResult, _ := json.Marshal(sessionMessage{
		Type: "rpc-result", SessionID: open.SessionID, RequestID: "upload-one", OK: true,
	})
	if err := worker.Write(ctx, websocket.MessageText, uploadResult); err != nil {
		t.Fatal(err)
	}
	_, uploadResultPayload, err := browser.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(uploadResultPayload), `"requestId":"upload-one"`) {
		t.Fatalf("unexpected browser upload result: %s", uploadResultPayload)
	}
}

func TestSandboxSessionForwardsDesktopFrames(t *testing.T) {
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

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/sandboxes/sandbox-one/desktop-ticket", nil)
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

	browser, _, err := websocket.Dial(ctx, websocketTestURL(server.URL, "/api/sandboxes/sandbox-one/desktop?ticket="+ticketBody.Ticket), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":           []string{"http://agentbox.example:3000"},
			"X-Forwarded-Host": []string{"agentbox.example:3000"},
		},
	})
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
	if open.Type != "open" || open.Mode != "desktop" || open.SessionID == "" {
		t.Fatalf("unexpected desktop open message: %+v", open)
	}

	clientFrame := []byte{1, 2, 3, 4}
	if err := browser.Write(ctx, websocket.MessageBinary, clientFrame); err != nil {
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
	decoded, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		t.Fatal(err)
	}
	if input.Type != "desktop-input" || input.SessionID != open.SessionID || string(decoded) != string(clientFrame) {
		t.Fatalf("unexpected desktop input message: %+v", input)
	}

	serverFrame := []byte("RFB 003.008\n")
	output, _ := json.Marshal(sessionMessage{
		Type: "desktop-data", SessionID: open.SessionID, Data: base64.StdEncoding.EncodeToString(serverFrame),
	})
	if err := worker.Write(ctx, websocket.MessageText, output); err != nil {
		t.Fatal(err)
	}
	messageType, outputPayload, err := browser.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary || string(outputPayload) != string(serverFrame) {
		t.Fatalf("unexpected browser desktop frame: type=%v payload=%q", messageType, outputPayload)
	}
}

func TestValidBrowserSessionMessageAcceptsChunkedUploads(t *testing.T) {
	uploadID := "3e8774b5-4921-4c8c-8927-2b7a30d518d2"
	for _, operation := range []string{"upload-start", "upload-chunk", "upload-finish", "upload-cancel"} {
		payload, err := json.Marshal(sessionMessage{
			Type: "rpc", RequestID: "request-one", Operation: operation,
			Path: "/workspace/example.bin", UploadID: uploadID,
			Content: func() string {
				if operation == "upload-chunk" {
					return "YWdlbnRib3g="
				}
				return ""
			}(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := validBrowserSessionMessage(payload); !ok {
			t.Fatalf("operation %q was rejected", operation)
		}
	}
}

func TestValidBrowserSessionMessageRejectsInvalidUploads(t *testing.T) {
	for name, message := range map[string]sessionMessage{
		"invalid id": {
			Type: "rpc", RequestID: "request-one", Operation: "upload-start",
			Path: "/workspace/example.bin", UploadID: "not-a-uuid",
		},
		"oversized chunk": {
			Type: "rpc", RequestID: "request-one", Operation: "upload-chunk",
			Path: "/workspace/example.bin", UploadID: "3e8774b5-4921-4c8c-8927-2b7a30d518d2",
			Content: strings.Repeat("a", 384*1024+1),
		},
	} {
		payload, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := validBrowserSessionMessage(payload); ok {
			t.Fatalf("%s was accepted", name)
		}
	}
}
