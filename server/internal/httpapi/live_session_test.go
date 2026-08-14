package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestLiveSandboxSession(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("AGENTBOX_LIVE_SESSION_URL"), "/")
	sandboxID := os.Getenv("AGENTBOX_LIVE_SANDBOX_ID")
	if baseURL == "" || sandboxID == "" {
		t.Skip("set AGENTBOX_LIVE_SESSION_URL and AGENTBOX_LIVE_SANDBOX_ID to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/sandboxes/"+sandboxID+"/session-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var ticket struct {
		Ticket string `json:"ticket"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&ticket); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d error = %q", response.StatusCode, ticket.Error)
	}

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/api/sandboxes/" + sandboxID + "/session?ticket=" + ticket.Ticket
	connection, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if _, err := readLiveSessionUntil(ctx, connection, func(message sessionMessage, _ string) bool {
		return message.Type == "ready"
	}); err != nil {
		t.Fatal(err)
	}

	writeLiveSession(t, ctx, connection, map[string]any{"type": "resize", "cols": 132, "rows": 41})
	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "stty size\r"})
	if output, err := readLiveSessionUntil(ctx, connection, func(_ sessionMessage, output string) bool {
		return strings.Contains(output, "41 132")
	}); err != nil {
		t.Fatalf("PTY resize: %v\n%s", err, output)
	}

	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "test -t 0 && test -t 1 && echo PTY_OK\r"})
	if output, err := readLiveSessionUntil(ctx, connection, func(_ sessionMessage, output string) bool {
		return strings.Contains(output, "PTY_OK")
	}); err != nil {
		t.Fatalf("PTY check: %v\n%s", err, output)
	}

	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "stty -echo\r"})
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "printf '\\nLATENCY_OK\\n'; stty echo\r"})
	if output, err := readLiveSessionUntil(ctx, connection, func(_ sessionMessage, output string) bool {
		return strings.Contains(output, "LATENCY_OK")
	}); err != nil {
		t.Fatalf("latency probe: %v\n%s", err, output)
	}
	t.Logf("interactive round trip: %s", time.Since(started))

	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "sleep 30\r"})
	time.Sleep(150 * time.Millisecond)
	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "\x03"})
	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "echo INTERRUPT_OK\r"})
	if output, err := readLiveSessionUntil(ctx, connection, func(_ sessionMessage, output string) bool {
		return strings.Contains(output, "INTERRUPT_OK")
	}); err != nil {
		t.Fatalf("Ctrl+C: %v\n%s", err, output)
	}

	writeLiveSession(t, ctx, connection, map[string]any{
		"type": "rpc", "requestId": "write-one", "operation": "write",
		"path": "/tmp/agentbox-session-e2e.txt", "content": "session-rpc-ok\n",
	})
	if _, err := readLiveSessionUntil(ctx, connection, func(message sessionMessage, _ string) bool {
		return message.Type == "rpc-result" && message.RequestID == "write-one" && message.OK
	}); err != nil {
		t.Fatal(err)
	}
	writeLiveSession(t, ctx, connection, map[string]any{
		"type": "rpc", "requestId": "read-one", "operation": "read",
		"path": "/tmp/agentbox-session-e2e.txt",
	})
	if output, err := readLiveSessionUntil(ctx, connection, func(message sessionMessage, _ string) bool {
		return message.Type == "rpc-result" && message.RequestID == "read-one" && strings.Contains(string(message.Result), "session-rpc-ok")
	}); err != nil {
		t.Fatalf("file RPC: %v\n%s", err, output)
	}
	writeLiveSession(t, ctx, connection, map[string]any{"type": "input", "data": "rm -f /tmp/agentbox-session-e2e.txt\r"})
}

func writeLiveSession(t *testing.T, ctx context.Context, connection *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readLiveSessionUntil(
	ctx context.Context,
	connection *websocket.Conn,
	done func(sessionMessage, string) bool,
) (string, error) {
	var output strings.Builder
	for {
		_, payload, err := connection.Read(ctx)
		if err != nil {
			return output.String(), err
		}
		var message sessionMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return output.String(), fmt.Errorf("decode session frame: %w", err)
		}
		if message.Type == "error" {
			return output.String(), fmt.Errorf("session error: %s", message.Error)
		}
		if message.Type == "output" {
			output.WriteString(message.Data)
		}
		if done(message, output.String()) {
			return output.String(), nil
		}
	}
}
