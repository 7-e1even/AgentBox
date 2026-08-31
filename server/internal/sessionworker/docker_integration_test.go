//go:build linux

package sessionworker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

func TestDockerSessionWorkerDataPlaneE2E(t *testing.T) {
	if os.Getenv("AGENTBOX_DOCKER_E2E") != "1" {
		t.Skip("set AGENTBOX_DOCKER_E2E=1 to run the Docker Session Worker integration test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("Docker CLI is required: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	const image = "alpine:3.20"
	container := "agentbox-e2e-" + uuid.NewString()
	runDocker(t, ctx, "pull", image)
	runDocker(t, ctx, "create", "--name", container, image, "sleep", "300")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "-f", container).Run()
	})
	runDocker(t, ctx, "start", container)

	if err := runtimeInspect(ctx, "docker", container); err != nil {
		t.Fatalf("inspect started container: %v", err)
	}

	// Container lifecycle setup is intentionally direct Docker CLI. The
	// production data-plane proof begins here: runConnection establishes the
	// authenticated Worker WebSocket, opens a real terminal, and routes file
	// operations through sessionManager.handleRPC into the container.
	workerConn, stopWorker := connectDockerSessionWorker(t, ctx)
	defer stopWorker()
	sessionID := uuid.NewString()
	if err := wsjson.Write(ctx, workerConn, message{
		Type: "open", SessionID: sessionID, ExternalID: container,
		Driver: "docker", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("open Docker Worker session: %v", err)
	}
	waitForWorkerMessage(t, ctx, workerConn, func(incoming message) bool {
		return incoming.Type == "ready" && incoming.SessionID == sessionID
	})

	const path = "/tmp/agentbox-e2e.txt"
	const written = "agentbox-worker-docker-e2e\n"
	writeRequestID := uuid.NewString()
	if err := wsjson.Write(ctx, workerConn, message{
		Type: "rpc", SessionID: sessionID, RequestID: writeRequestID,
		Operation: "write", Path: path, Content: written,
	}); err != nil {
		t.Fatalf("send Worker write RPC: %v", err)
	}
	writeResult := waitForWorkerMessage(t, ctx, workerConn, func(incoming message) bool {
		return incoming.Type == "rpc-result" && incoming.RequestID == writeRequestID
	})
	if !writeResult.OK || writeResult.Error != "" || writeResult.Result != "saved" {
		t.Fatalf("Worker write RPC result = %#v", writeResult)
	}

	readRequestID := uuid.NewString()
	if err := wsjson.Write(ctx, workerConn, message{
		Type: "rpc", SessionID: sessionID, RequestID: readRequestID,
		Operation: "read", Path: path,
	}); err != nil {
		t.Fatalf("send Worker read RPC: %v", err)
	}
	readResult := waitForWorkerMessage(t, ctx, workerConn, func(incoming message) bool {
		return incoming.Type == "rpc-result" && incoming.RequestID == readRequestID
	})
	if !readResult.OK || readResult.Error != "" || readResult.Result != written {
		t.Fatalf("Worker read RPC result = %#v, want content %q", readResult, written)
	}
	if err := wsjson.Write(ctx, workerConn, message{Type: "close", SessionID: sessionID}); err != nil {
		t.Fatalf("close Docker Worker session: %v", err)
	}
	closedRequestID := uuid.NewString()
	if err := wsjson.Write(ctx, workerConn, message{
		Type: "rpc", SessionID: sessionID, RequestID: closedRequestID,
		Operation: "read", Path: path,
	}); err != nil {
		t.Fatalf("probe closed Docker Worker session: %v", err)
	}
	closedResult := waitForWorkerMessage(t, ctx, workerConn, func(incoming message) bool {
		return incoming.Type == "rpc-result" && incoming.RequestID == closedRequestID
	})
	if closedResult.OK || closedResult.Error != "terminal session is not ready" {
		t.Fatalf("closed Worker session RPC result = %#v", closedResult)
	}

	runDocker(t, ctx, "rm", "-f", container)
	if err := runtimeInspect(ctx, "docker", container); err == nil {
		t.Fatal("deleted container is still inspectable")
	}
}

func connectDockerSessionWorker(t *testing.T, ctx context.Context) (*websocket.Conn, func()) {
	t.Helper()
	const (
		serverID   = "docker-e2e-server"
		credential = "docker-e2e-credential"
	)
	connections := make(chan *websocket.Conn, 1)
	connectionErrors := make(chan error, 1)
	serverStop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/servers/"+serverID+"/sessions/connect" {
			connectionErrors <- fmt.Errorf("Worker connected to unexpected path %q", request.URL.Path)
			http.NotFound(response, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+credential {
			connectionErrors <- fmt.Errorf("Worker sent unexpected authorization header")
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("User-Agent") != "AgentBox-Session-Worker/3" {
			connectionErrors <- fmt.Errorf("Worker sent unexpected user agent %q", request.Header.Get("User-Agent"))
			http.Error(response, "invalid user agent", http.StatusBadRequest)
			return
		}
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			connectionErrors <- fmt.Errorf("accept Worker WebSocket: %w", err)
			return
		}
		defer connection.CloseNow()
		connections <- connection
		<-serverStop
	}))

	configPath := filepath.Join(t.TempDir(), "worker.conf")
	config := server.URL + "\n" + serverID + "\n" + credential + "\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		close(serverStop)
		server.Close()
		t.Fatalf("write Worker configuration: %v", err)
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	type connectionResult struct {
		connected bool
		err       error
	}
	workerResult := make(chan connectionResult, 1)
	go func() {
		connected, err := runConnection(workerCtx, configPath)
		workerResult <- connectionResult{connected: connected, err: err}
	}()

	var connection *websocket.Conn
	select {
	case connection = <-connections:
	case err := <-connectionErrors:
		cancelWorker()
		close(serverStop)
		server.Close()
		t.Fatal(err)
	case <-ctx.Done():
		cancelWorker()
		close(serverStop)
		server.Close()
		t.Fatalf("wait for Worker WebSocket: %v", ctx.Err())
	}

	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancelWorker()
		select {
		case result := <-workerResult:
			if !result.connected {
				t.Errorf("Worker connection never became active: %v", result.err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Worker connection did not stop after cancellation")
		}
		close(serverStop)
		server.Close()
	}
	return connection, stop
}

func waitForWorkerMessage(t *testing.T, ctx context.Context, connection *websocket.Conn, matches func(message) bool) message {
	t.Helper()
	for {
		var incoming message
		if err := wsjson.Read(ctx, connection, &incoming); err != nil {
			t.Fatalf("read Worker message: %v", err)
		}
		if incoming.Type == "error" {
			t.Fatalf("Worker session error: %s", incoming.Error)
		}
		if matches(incoming) {
			return incoming
		}
	}
}

func runDocker(t *testing.T, ctx context.Context, args ...string) {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
