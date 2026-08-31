package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
)

type resourceQueryStore struct {
	fakeStore
	filter platform.ResourceFilter
	id     string
}

func (s *resourceQueryStore) ListResourcesFiltered(_ context.Context, filter platform.ResourceFilter) ([]platform.Resource, error) {
	s.filter = filter
	return []platform.Resource{}, nil
}

func (s *resourceQueryStore) GetResource(_ context.Context, id string) (platform.Resource, error) {
	s.id = id
	return platform.Resource{Input: platform.Input{ID: id, Kind: platform.KindSandbox, SpecVersion: 1}, Generation: 2, ObservedGeneration: 1}, nil
}

func TestResourceQueriesUseScopedStoreMethods(t *testing.T) {
	repository := &resourceQueryStore{}
	server := New(repository, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{DisableAuth: true})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/resources?kind=runtime&projectId=default", nil))
	if response.Code != http.StatusOK || repository.filter != (platform.ResourceFilter{Kind: platform.KindRuntime, ProjectID: "default"}) {
		t.Fatalf("list response = %d, filter = %#v", response.Code, repository.filter)
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/resources?kind=unknown", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/resources/sandbox-1", nil))
	var body struct {
		Resource platform.Resource `json:"resource"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || repository.id != "sandbox-1" || body.Resource.Generation != 2 || body.Resource.ObservedGeneration != 1 {
		t.Fatalf("get response = %d, id = %s, body = %#v", response.Code, repository.id, body)
	}
}
