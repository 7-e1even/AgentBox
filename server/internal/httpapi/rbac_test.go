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
)

// rbacStore 复用 server_test.go 的 fakeStore，仅覆盖会话解析以返回指定角色。
type rbacStore struct {
	fakeStore
	user platform.User
}

func (s rbacStore) UserBySession(context.Context, []byte) (platform.User, error) {
	return s.user, nil
}

func rbacHandler(role platform.UserRole) http.Handler {
	user := testAdmin()
	user.Role = role
	handler := New(rbacStore{user: user}, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
		handler.ServeHTTP(w, request)
	})
}

func rbacRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestViewerWriteRequestsAreForbidden(t *testing.T) {
	handler := rbacHandler(platform.UserRoleViewer)
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/resources", `{"kind":"sandbox","name":"x","spec":{}}`},
		{http.MethodPatch, "/api/resources/sandbox-one", `{"kind":"sandbox","name":"x","spec":{}}`},
		{http.MethodDelete, "/api/resources/sandbox-one", ""},
		{http.MethodPost, "/api/sandboxes/sandbox-one/actions/start", ""},
		{http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", ""},
		{http.MethodPost, "/api/server-pairings", ""},
		{http.MethodDelete, "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310", ""},
		{http.MethodPost, "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/actions/update-worker", ""},
		{http.MethodPost, "/api/automations", `{"projectId":"default","name":"a","trigger":{"type":"webhook"},"templateId":"t"}`},
		{http.MethodGet, "/api/automations/5f7a65c5-1df2-4ac3-bdbf-753af92ac388/secret", ""},
		{http.MethodDelete, "/api/automations/5f7a65c5-1df2-4ac3-bdbf-753af92ac388", ""},
		{http.MethodPost, "/api/credentials", `{"providerId":"openai","name":"k","apiKey":"sk-test"}`},
		{http.MethodDelete, "/api/credentials/openai-primary", ""},
		{http.MethodDelete, "/api/credentials/openai-primary/models/custom-model", ""},
		{http.MethodPost, "/api/network-proxies", `{"name":"p","url":"http://proxy:8080"}`},
		{http.MethodDelete, "/api/network-proxies/proxy-one", ""},
		{http.MethodGet, "/api/users", ""},
		{http.MethodDelete, "/api/users/0954c4cd-8cce-4f3f-9d73-f03407c9afe1", ""},
		{http.MethodGet, "/api/server-pairings/a5bd3926-fecf-49aa-802a-5ed3f1a42fd0", ""},
	}
	for _, testCase := range cases {
		response := rbacRequest(t, handler, testCase.method, testCase.path, testCase.body)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d (body = %s)", testCase.method, testCase.path, response.Code, http.StatusForbidden, response.Body.String())
		}
	}
}

func TestViewerReadRequestsAreAllowed(t *testing.T) {
	handler := rbacHandler(platform.UserRoleViewer)
	cases := []string{
		"/api/catalog",
		"/api/resources",
		"/api/automations?projectId=default",
		"/api/automation-runs?projectId=default",
		"/api/credentials",
		"/api/network-proxies",
		"/api/servers",
		"/api/auth/me",
	}
	for _, path := range cases {
		response := rbacRequest(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d (body = %s)", path, response.Code, http.StatusOK, response.Body.String())
		}
	}
}

func TestOperatorSandboxOperationsAreAllowed(t *testing.T) {
	handler := rbacHandler(platform.UserRoleOperator)

	response := rbacRequest(t, handler, http.MethodPost, "/api/sandboxes/sandbox-one/actions/start", "")
	if response.Code != http.StatusAccepted {
		t.Errorf("sandbox action: status = %d, want %d (body = %s)", response.Code, http.StatusAccepted, response.Body.String())
	}

	response = rbacRequest(t, handler, http.MethodPost, "/api/resources",
		`{"kind":"sandbox","name":"x","spec":{}}`)
	if response.Code != http.StatusCreated {
		t.Errorf("create resource: status = %d, want %d (body = %s)", response.Code, http.StatusCreated, response.Body.String())
	}

	// session-ticket 对 operator 放行（fake 中没有运行中的沙箱，因此落到业务错误而非 403）。
	response = rbacRequest(t, handler, http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", "")
	if response.Code == http.StatusForbidden || response.Code == http.StatusUnauthorized {
		t.Errorf("session-ticket: status = %d, 不应被角色门禁拦截 (body = %s)", response.Code, response.Body.String())
	}

	response = rbacRequest(t, handler, http.MethodPost, "/api/automations",
		`{"projectId":"default","name":"a","trigger":{"type":"webhook"},"templateId":"t"}`)
	if response.Code != http.StatusCreated {
		t.Errorf("create automation: status = %d, want %d (body = %s)", response.Code, http.StatusCreated, response.Body.String())
	}

	response = rbacRequest(t, handler, http.MethodGet,
		"/api/automations/5f7a65c5-1df2-4ac3-bdbf-753af92ac388/secret", "")
	if response.Code != http.StatusOK {
		t.Errorf("read automation secret: status = %d, want %d (body = %s)", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestOperatorAdminOnlyAreasAreForbidden(t *testing.T) {
	handler := rbacHandler(platform.UserRoleOperator)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodPost, "/api/server-pairings"},
		{http.MethodGet, "/api/server-pairings/a5bd3926-fecf-49aa-802a-5ed3f1a42fd0"},
		{http.MethodDelete, "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310"},
		{http.MethodPost, "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/actions/update-worker"},
		{http.MethodPost, "/api/credentials"},
		{http.MethodDelete, "/api/credentials/openai-primary"},
		{http.MethodDelete, "/api/credentials/openai-primary/models/custom-model"},
		{http.MethodPost, "/api/network-proxies"},
		{http.MethodDelete, "/api/network-proxies/proxy-one"},
	}
	for _, testCase := range cases {
		response := rbacRequest(t, handler, testCase.method, testCase.path, "")
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d (body = %s)", testCase.method, testCase.path, response.Code, http.StatusForbidden, response.Body.String())
		}
	}
}

func TestAdminRetainsFullAccess(t *testing.T) {
	handler := rbacHandler(platform.UserRoleAdmin)

	response := rbacRequest(t, handler, http.MethodPost, "/api/server-pairings", "")
	if response.Code != http.StatusCreated {
		t.Errorf("create pairing: status = %d, want %d (body = %s)", response.Code, http.StatusCreated, response.Body.String())
	}

	response = rbacRequest(t, handler, http.MethodGet, "/api/users", "")
	if response.Code != http.StatusOK {
		t.Errorf("list users: status = %d, want %d (body = %s)", response.Code, http.StatusOK, response.Body.String())
	}

	response = rbacRequest(t, handler, http.MethodPost, "/api/sandboxes/sandbox-one/actions/stop", "")
	if response.Code != http.StatusAccepted {
		t.Errorf("sandbox action: status = %d, want %d (body = %s)", response.Code, http.StatusAccepted, response.Body.String())
	}
}

func TestUnauthenticatedRequestsStillRequireLogin(t *testing.T) {
	handler := New(fakeStore{}, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
	request := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
