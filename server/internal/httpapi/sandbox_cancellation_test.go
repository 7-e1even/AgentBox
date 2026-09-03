package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type cancellationControlStore struct {
	fakeStore
	serverID, credential, jobID string
	input                       platform.WorkerJobControlInput
	called                      bool
	err                         error
}

func (s *cancellationControlStore) ControlWorkerJob(_ context.Context, serverID, credential, jobID string, input platform.WorkerJobControlInput) (platform.WorkerJobControl, error) {
	s.serverID, s.credential, s.jobID, s.input, s.called = serverID, credential, jobID, input, true
	return platform.WorkerJobControl{CancelRequested: true}, s.err
}

func TestWorkerCancellationControlValidatesExactGenerationAndForwardsAuthentication(t *testing.T) {
	for _, test := range []struct {
		body string
		code int
	}{
		{`{"leaseGeneration":3}`, http.StatusOK},
		{`{"leaseGeneration":0}`, http.StatusBadRequest},
		{`{"leaseGeneration":-1}`, http.StatusBadRequest},
		{`{}`, http.StatusBadRequest},
		{`{"leaseGeneration":"1"}`, http.StatusBadRequest},
	} {
		t.Run(test.body, func(t *testing.T) {
			storage := &cancellationControlStore{}
			handler := New(storage, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
			request := httptest.NewRequest(http.MethodPost, "/api/servers/server-id/jobs/job-id/control", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer worker-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.code {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if test.code == http.StatusOK {
				if !storage.called || storage.serverID != "server-id" || storage.jobID != "job-id" || storage.credential != "worker-token" || storage.input.LeaseGeneration != 3 ||
					!strings.Contains(response.Body.String(), `"cancelRequested":true`) {
					t.Fatalf("control authentication/response contract = %+v, %s", storage, response.Body.String())
				}
			} else if storage.called {
				t.Fatal("invalid control reached the store")
			}
		})
	}
}

func TestWorkerCancellationControlRejectsInvalidWorkerCredential(t *testing.T) {
	storage := &cancellationControlStore{err: store.ErrWorkerUnauthorized}
	handler := New(storage, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server-id/jobs/job-id/control", strings.NewReader(`{"leaseGeneration":1}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control status = %d", response.Code)
	}
}

func TestSandboxCancellationRequiresOperatorRole(t *testing.T) {
	for _, role := range []platform.UserRole{platform.UserRoleViewer, platform.UserRoleOperator, platform.UserRoleAdmin} {
		response := rbacRequest(t, rbacHandler(role), http.MethodPost, "/api/sandboxes/sandbox-id/actions/cancel-install", "")
		want := http.StatusAccepted
		if role == platform.UserRoleViewer {
			want = http.StatusForbidden
		}
		if response.Code != want {
			t.Fatalf("role %s cancellation status = %d, want %d: %s", role, response.Code, want, response.Body.String())
		}
	}
}
