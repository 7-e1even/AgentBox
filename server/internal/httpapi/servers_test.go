package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type workerUpdateStore struct {
	fakeStore
	serverID string
	version  string
}

func (s *workerUpdateStore) EnqueueWorkerUpdate(_ context.Context, serverID, version string) error {
	s.serverID = serverID
	s.version = version
	return nil
}

func TestUpdateWorkerEnqueuesConfiguredReleaseVersion(t *testing.T) {
	store := &workerUpdateStore{}
	server := &Server{store: store, workerVersion: "v1.2.3"}
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server/actions/update-worker", strings.NewReader(`{}`))
	request.SetPathValue("id", "79e642fc-5ae8-41f9-a609-a29f26f591e9")
	response := httptest.NewRecorder()
	server.updateWorker(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
	}
	if store.serverID != "79e642fc-5ae8-41f9-a609-a29f26f591e9" || store.version != "v1.2.3" {
		t.Fatalf("enqueued server = %q version = %q", store.serverID, store.version)
	}
}

func TestUpdateWorkerRejectsDevelopmentVersion(t *testing.T) {
	server := &Server{store: &workerUpdateStore{}, workerVersion: "dev"}
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server/actions/update-worker", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	server.updateWorker(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestUpdateWorkerRejectsVersionOutsideCurrentServerRelease(t *testing.T) {
	store := &workerUpdateStore{}
	server := &Server{store: store, workerVersion: "v1.2.3"}
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server/actions/update-worker", strings.NewReader(`{"version":"v1.2.2"}`))
	response := httptest.NewRecorder()
	server.updateWorker(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if store.version != "" {
		t.Fatalf("unexpected enqueued version %q", store.version)
	}
}
