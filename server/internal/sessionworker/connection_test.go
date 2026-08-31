package sessionworker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentbox/internal/workerprotocol"
	"github.com/coder/websocket"
)

func TestSessionWorkerNegotiatesActualWebSocket(t *testing.T) {
	for _, test := range []struct {
		name, selected string
		status         int
		wantError      bool
	}{
		{name: "current", selected: "1"},
		{name: "n-1 Server without header"},
		{name: "out of range selection", selected: "2", wantError: true},
		{name: "invalid selection", selected: "invalid", wantError: true},
		{name: "Server rejects range", status: http.StatusUpgradeRequired, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Header.Get(workerprotocol.HeaderMinimum) != "1" || request.Header.Get(workerprotocol.HeaderMaximum) != "1" {
					t.Error("Session Worker did not offer its protocol range")
				}
				if request.Header.Get("Authorization") != "Bearer test-credential" {
					t.Error("Session Worker did not authenticate")
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
					return
				}
				if test.selected != "" {
					w.Header().Set(workerprotocol.HeaderSelected, test.selected)
				}
				conn, err := websocket.Accept(w, request, nil)
				if err != nil {
					t.Errorf("accept: %v", err)
					return
				}
				defer conn.CloseNow()
				_, _, _ = conn.Read(ctx)
			}))
			defer server.Close()
			conn, err := dialConnection(ctx, workerConfig{serverURL: server.URL, serverID: "server-one", credential: "test-credential"})
			if test.wantError {
				if conn != nil {
					conn.CloseNow()
				}
				if !errors.Is(err, workerprotocol.ErrIncompatible) {
					t.Fatalf("dial error = %v, want incompatible", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			conn.CloseNow()
		})
	}
}
