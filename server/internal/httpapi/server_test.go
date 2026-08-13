package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/platform"
)

type fakeStore struct{}

func (fakeStore) List(context.Context) ([]agent.Agent, error)      { return []agent.Agent{}, nil }
func (fakeStore) Get(context.Context, string) (agent.Agent, error) { return agent.Agent{}, nil }
func (fakeStore) Create(_ context.Context, input agent.Input) (agent.Agent, error) {
	return agent.Agent{Input: input, ID: "b88eb8db-3954-4d1a-bda3-005e1fb375c4", Version: 1}, nil
}
func (fakeStore) Update(context.Context, string, agent.Input, int) (agent.Agent, error) {
	return agent.Agent{}, nil
}
func (fakeStore) Duplicate(context.Context, string) (agent.Agent, error) { return agent.Agent{}, nil }
func (fakeStore) Delete(context.Context, string) error                   { return nil }
func (fakeStore) ListResources(context.Context) ([]platform.Resource, error) {
	return []platform.Resource{}, nil
}
func (fakeStore) CreateResource(_ context.Context, input platform.Input) (platform.Resource, error) {
	return platform.Resource{Input: input}, nil
}
func (fakeStore) UpdateResource(_ context.Context, _ string, input platform.Input) (platform.Resource, error) {
	return platform.Resource{Input: input}, nil
}
func (fakeStore) DeleteResource(context.Context, string) error { return nil }
func (fakeStore) Ping(context.Context) error                   { return nil }

func testHandler() http.Handler {
	return New(fakeStore{}, agent.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func TestCatalogEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"providers"`) {
		t.Fatalf("body does not contain catalog: %s", response.Body.String())
	}
}

func TestInvalidAgentIDReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/agents/not-a-uuid", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestCreateRejectsUnknownJSONFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
