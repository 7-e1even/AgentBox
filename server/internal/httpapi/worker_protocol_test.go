package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentbox/internal/workerprotocol"
)

func TestWorkerProtocolRejectsBeforeStoreOrBodyHandling(t *testing.T) {
	server := &Server{} // Any store access would panic, so no job can be claimed.
	for name, handler := range map[string]http.HandlerFunc{
		"claim": server.claimWorkerJob, "progress": server.reportWorkerJobProgress,
		"complete": server.completeWorkerJob, "session": server.connectWorkerSessions,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set(workerprotocol.HeaderMinimum, "2")
			request.Header.Set(workerprotocol.HeaderMaximum, "2")
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != http.StatusUpgradeRequired {
				t.Fatalf("status = %d, want 426: %s", response.Code, response.Body.String())
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "worker_protocol_incompatible" || body.Error.Retryable {
				t.Fatalf("unexpected error contract: %#v", body.Error)
			}
		})
	}
}

func TestWorkerClaimNegotiatesCurrentAndNMinusOne(t *testing.T) {
	for _, test := range []struct{ name, minimum, maximum string }{
		{name: "current", minimum: "1", maximum: "1"},
		{name: "wider compatible range", minimum: "1", maximum: "2"},
		{name: "n-1 without headers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/servers/server-one/jobs/claim", nil)
			if test.minimum != "" {
				request.Header.Set(workerprotocol.HeaderMinimum, test.minimum)
				request.Header.Set(workerprotocol.HeaderMaximum, test.maximum)
			}
			response := httptest.NewRecorder()
			testHandler().ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || response.Header().Get(workerprotocol.HeaderSelected) != "1" {
				t.Fatalf("status = %d, selected = %q, body = %s", response.Code, response.Header().Get(workerprotocol.HeaderSelected), response.Body.String())
			}
		})
	}
}

func TestWorkerProtocolRejectsMalformedRange(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server-one/jobs/claim", nil)
	request.Header.Set(workerprotocol.HeaderMinimum, "1")
	response := httptest.NewRecorder()
	(&Server{}).claimWorkerJob(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestHealthDeclaresSingleInstanceSessionTopology(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body["sessionTopology"] != "single-instance" {
		t.Fatalf("health = %d %#v", response.Code, body)
	}
}
