package httpapi

import (
	"context"
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
	"agentbox/internal/store"
)

type writeDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (r *writeDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.deadline = deadline
	return nil
}

func TestStatusRecorderUnwrapsForStreamingWriteDeadline(t *testing.T) {
	underlying := &writeDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	recorder := &statusRecorder{ResponseWriter: underlying, status: http.StatusOK}
	want := time.Now().Add(time.Hour).Round(time.Second)
	if err := http.NewResponseController(recorder).SetWriteDeadline(want); err != nil {
		t.Fatal(err)
	}
	if !underlying.deadline.Equal(want) {
		t.Fatalf("write deadline = %v, want %v", underlying.deadline, want)
	}
}

func TestWriteErrorUsesStructuredContract(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		code      string
		retryable bool
	}{
		{name: "client error", status: http.StatusBadRequest, code: "invalid_request"},
		{name: "temporary server error", status: http.StatusServiceUnavailable, code: "service_unavailable", retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{}
			response := httptest.NewRecorder()
			server.writeError(response, test.status, "请求失败原因")

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != test.code || body.Error.Message != "请求失败原因" || body.Error.Retryable != test.retryable {
				t.Fatalf("error = %#v", body.Error)
			}
		})
	}
}

type fakeStore struct{}

type emptyUsersStore struct{ fakeStore }

func (emptyUsersStore) ListUsers(context.Context) ([]platform.User, error) {
	return []platform.User{}, nil
}

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
func (fakeStore) OperateSandbox(_ context.Context, id, action string) (platform.Resource, error) {
	switch action {
	case "start", "stop", "restart", "delete":
	default:
		return platform.Resource{}, &platform.ValidationError{Message: "不支持的沙箱操作"}
	}
	return platform.Resource{Input: platform.Input{ID: id, Kind: platform.KindSandbox, Name: action}}, nil
}
func (fakeStore) UpdateSandboxNetworkProxy(_ context.Context, id, proxyID string) (platform.Resource, error) {
	return platform.Resource{Input: platform.Input{
		ID: id, Kind: platform.KindSandbox, Name: "proxy",
		Spec: map[string]any{"proxyId": proxyID},
	}}, nil
}
func (fakeStore) OperateSandboxAgentTools(_ context.Context, id, action string, tools []string) (platform.Resource, error) {
	switch action {
	case "check", "update":
	default:
		return platform.Resource{}, &platform.ValidationError{Message: "不支持的 Agent 工具操作"}
	}
	return platform.Resource{Input: platform.Input{
		ID: id, Kind: platform.KindSandbox, Name: action,
		Spec: map[string]any{"requestedAgentTools": tools},
	}}, nil
}
func (fakeStore) ListAutomations(context.Context, string) ([]platform.Automation, error) {
	return []platform.Automation{}, nil
}
func (fakeStore) GetAutomation(context.Context, string) (platform.Automation, error) {
	return platform.Automation{ID: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388", ProjectID: "default"}, nil
}
func (fakeStore) GetAutomationSecret(context.Context, string) (platform.Automation, string, error) {
	return platform.Automation{
		ID: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388", Name: "PR Preview",
		EndpointID: "75778270-bdbf-4e2f-bbeb-b3133447a367",
	}, "abx_wh_visible", nil
}
func (fakeStore) CreateAutomation(_ context.Context, input platform.AutomationInput, userID string) (platform.Automation, string, error) {
	return platform.Automation{
		ID: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388", ProjectID: input.ProjectID,
		Name: input.Name, Description: input.Description, Enabled: input.Enabled,
		Trigger: input.Trigger, TemplateID: input.TemplateID,
		EndpointID: "75778270-bdbf-4e2f-bbeb-b3133447a367", SecretLastFour: "test",
	}, "abx_wh_test", nil
}
func (fakeStore) UpdateAutomation(_ context.Context, id string, input platform.AutomationInput, userID string) (platform.Automation, error) {
	return platform.Automation{
		ID: id, ProjectID: input.ProjectID, Name: input.Name, Description: input.Description,
		Enabled: input.Enabled, Trigger: input.Trigger, TemplateID: input.TemplateID,
	}, nil
}
func (fakeStore) DeleteAutomation(context.Context, string) error { return nil }
func (fakeStore) RotateAutomationSecret(context.Context, string, string) (platform.Automation, string, error) {
	return platform.Automation{ID: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388", EndpointID: "75778270-bdbf-4e2f-bbeb-b3133447a367"}, "abx_wh_rotated", nil
}
func (fakeStore) TriggerAutomation(context.Context, platform.AutomationDelivery) (platform.AutomationTriggerResult, error) {
	sandboxID := "auto-webhook"
	return platform.AutomationTriggerResult{Run: platform.AutomationRun{
		ID: "96b47a49-96d4-492d-85e6-8c14f0315ae8", EndpointID: "75778270-bdbf-4e2f-bbeb-b3133447a367", Status: platform.AutomationRunQueued,
		SandboxID: &sandboxID,
	}, StatusToken: "run-token"}, nil
}
func (fakeStore) TestAutomation(context.Context, string, []byte) (platform.AutomationTriggerResult, error) {
	return platform.AutomationTriggerResult{Run: platform.AutomationRun{
		ID: "96b47a49-96d4-492d-85e6-8c14f0315ae8", Status: platform.AutomationRunQueued,
	}}, nil
}
func (fakeStore) ListAutomationRunsPage(context.Context, platform.AutomationRunFilter) (platform.AutomationRunPage, error) {
	return platform.AutomationRunPage{Items: []platform.AutomationRun{}}, nil
}
func (fakeStore) GetAutomationRun(context.Context, string) (platform.AutomationRun, error) {
	return platform.AutomationRun{ID: "96b47a49-96d4-492d-85e6-8c14f0315ae8", Status: platform.AutomationRunSucceeded}, nil
}
func (fakeStore) GetPublicAutomationRun(context.Context, string, string, string) (platform.AutomationRun, error) {
	return platform.AutomationRun{ID: "96b47a49-96d4-492d-85e6-8c14f0315ae8", Status: platform.AutomationRunSucceeded}, nil
}
func (fakeStore) ListCredentials(context.Context) ([]platform.ManagedCredential, error) {
	return []platform.ManagedCredential{}, nil
}
func (fakeStore) CreateCredential(_ context.Context, input platform.CredentialInput) (platform.ManagedCredential, error) {
	return platform.ManagedCredential{ID: input.ID, Name: input.Name, ProviderID: input.ProviderID}, nil
}
func (fakeStore) UpdateCredential(_ context.Context, _ string, input platform.CredentialInput) (platform.ManagedCredential, error) {
	return platform.ManagedCredential{ID: input.ID, Name: input.Name, ProviderID: input.ProviderID}, nil
}
func (fakeStore) CheckCredential(_ context.Context, id string) (platform.ManagedCredential, error) {
	return platform.ManagedCredential{ID: id, Name: "Checked Key", ProviderID: "openai"}, nil
}
func (fakeStore) PullCredentialModels(context.Context, string) ([]platform.CredentialModel, error) {
	return []platform.CredentialModel{{ID: "gpt-5", Name: "GPT-5", Group: "openai", Source: "remote"}}, nil
}
func (fakeStore) AddCredentialModel(context.Context, string, platform.CredentialModelInput) ([]platform.CredentialModel, error) {
	return []platform.CredentialModel{{ID: "custom-model", Name: "Custom Model", Group: "custom", Source: "manual"}}, nil
}
func (fakeStore) DeleteCredentialModel(context.Context, string, string) ([]platform.CredentialModel, error) {
	return []platform.CredentialModel{}, nil
}
func (fakeStore) DeleteCredential(context.Context, string) error { return nil }
func (fakeStore) ListNetworkProxies(context.Context) ([]platform.ManagedNetworkProxy, error) {
	return []platform.ManagedNetworkProxy{}, nil
}
func (fakeStore) CreateNetworkProxy(_ context.Context, input platform.NetworkProxyInput) (platform.ManagedNetworkProxy, error) {
	return platform.ManagedNetworkProxy{ID: input.ID, Name: input.Name}, nil
}
func (fakeStore) UpdateNetworkProxy(_ context.Context, _ string, input platform.NetworkProxyInput) (platform.ManagedNetworkProxy, error) {
	return platform.ManagedNetworkProxy{ID: input.ID, Name: input.Name}, nil
}
func (fakeStore) CreateNetworkProxyCheck(_ context.Context, proxyID, serverID, target string) (platform.NetworkProxyCheck, error) {
	return platform.NetworkProxyCheck{
		ID: "754f76dd-2297-44e9-8204-a688be9be4a5", ProxyID: proxyID,
		ServerID: serverID, ServerName: "Worker One", Scope: "worker", Status: "pending", Target: target,
	}, nil
}
func (fakeStore) GetNetworkProxyCheck(_ context.Context, proxyID, checkID string) (platform.NetworkProxyCheck, error) {
	ok := true
	return platform.NetworkProxyCheck{
		ID: checkID, ProxyID: proxyID, ServerID: "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
		ServerName: "Worker One", Scope: "worker", Status: "completed", OK: &ok,
		Target: networkProxyCheckTarget,
	}, nil
}
func (fakeStore) DeleteNetworkProxy(context.Context, string) error { return nil }
func (fakeStore) ResolveRuntimeLLMTarget(context.Context, string, string, string) (platform.RuntimeLLMTarget, error) {
	return platform.RuntimeLLMTarget{}, store.ErrRuntimeUnauthorized
}
func (fakeStore) ClaimWorkerJob(context.Context, string, string) (platform.WorkerJob, error) {
	return platform.WorkerJob{}, store.ErrNoJob
}
func (fakeStore) ReportWorkerJobProgress(context.Context, string, string, string, platform.WorkerJobProgressInput) (platform.ProvisioningProgress, error) {
	return platform.ProvisioningProgress{}, nil
}
func (fakeStore) CompleteWorkerJob(context.Context, string, string, string, platform.WorkerJobResult) error {
	return nil
}
func (fakeStore) EnqueueWorkerUpdate(context.Context, string, string) error { return nil }
func (fakeStore) ListServers(context.Context) ([]platform.ManagedServer, error) {
	return []platform.ManagedServer{}, nil
}
func (fakeStore) CreateServerPairing(context.Context) (platform.ServerPairing, error) {
	return platform.ServerPairing{ID: "a5bd3926-fecf-49aa-802a-5ed3f1a42fd0", Token: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (fakeStore) GetServerPairing(context.Context, string) (platform.ServerPairing, error) {
	return platform.ServerPairing{}, nil
}
func (fakeStore) RegisterServer(context.Context, platform.ServerRegistration) (platform.ManagedServer, string, error) {
	return platform.ManagedServer{}, strings.Repeat("b", 32), nil
}
func (fakeStore) HeartbeatServer(context.Context, string, string, []string, *platform.ServerInventory, string) error {
	return nil
}
func (fakeStore) DeleteServer(context.Context, string) error   { return nil }
func (fakeStore) NeedsUserSetup(context.Context) (bool, error) { return false, nil }
func (fakeStore) SetupAdmin(_ context.Context, input platform.UserInput, _ []byte, _ time.Time) (platform.User, error) {
	return platform.User{ID: "7250fd43-8301-44d2-a03f-df4dcc65e499", Name: input.Name, Username: input.Username, Email: input.Email, Role: platform.UserRoleAdmin, Status: platform.UserStatusActive}, nil
}
func (fakeStore) AuthenticateUser(context.Context, string, string, []byte, time.Time) (platform.User, error) {
	return testAdmin(), nil
}
func (fakeStore) UserBySession(context.Context, []byte) (platform.User, error) {
	return testAdmin(), nil
}
func (fakeStore) DeleteSession(context.Context, []byte) error { return nil }
func (fakeStore) ListUsers(context.Context) ([]platform.User, error) {
	return []platform.User{testAdmin()}, nil
}
func (fakeStore) CreateUser(_ context.Context, input platform.UserInput) (platform.User, error) {
	return platform.User{ID: "0954c4cd-8cce-4f3f-9d73-f03407c9afe1", Name: input.Name, Username: input.Username, Email: input.Email, Role: input.Role, Status: input.Status}, nil
}
func (fakeStore) UpdateUser(_ context.Context, id string, input platform.UserInput) (platform.User, error) {
	return platform.User{ID: id, Name: input.Name, Username: input.Username, Email: input.Email, Role: input.Role, Status: input.Status}, nil
}
func (fakeStore) UpdateUserPreferences(_ context.Context, id string, input platform.UserPreferences) (platform.User, error) {
	user := testAdmin()
	user.ID = id
	user.Preferences = input
	return user, nil
}
func (fakeStore) DeleteUser(context.Context, string) error              { return nil }
func (fakeStore) InsertLogs(context.Context, []platform.LogEntry) error { return nil }
func (fakeStore) ListLogs(context.Context, platform.LogFilter) ([]platform.LogEntry, int, error) {
	return []platform.LogEntry{}, 0, nil
}
func (fakeStore) Ping(context.Context) error { return nil }

func rawTestHandler() http.Handler {
	return New(fakeStore{}, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
}

func debugTestHandler() http.Handler {
	return New(fakeStore{}, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{DisableAuth: true})
}

func emptyDebugTestHandler() http.Handler {
	return New(emptyUsersStore{}, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{DisableAuth: true})
}

func testHandler() http.Handler {
	handler := rawTestHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
		handler.ServeHTTP(w, request)
	})
}

func testAdmin() platform.User {
	return platform.User{
		ID: "7250fd43-8301-44d2-a03f-df4dcc65e499", Name: "Admin", Username: "admin", Email: "admin@agentbox.local",
		Role: platform.UserRoleAdmin, Status: platform.UserStatusActive, Preferences: platform.DefaultUserPreferences(),
	}
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

func TestCatalogRequiresAuthentication(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestCreateAutomationReturnsOneTimeSecret(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/automations", strings.NewReader(`{
    "projectId":"default",
    "name":"PR Preview",
    "description":"Create a sandbox",
    "enabled":true,
	"trigger":{"type":"webhook","authMode":"bearer"},
	"templateId":"runtime-one"
  }`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"secret":"abx_wh_test"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestGetAutomationSecretReturnsPlaintextWithoutCaching(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"/api/automations/5f7a65c5-1df2-4ac3-bdbf-753af92ac388/secret", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"secret":"abx_wh_visible"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestAutomationWebhookDoesNotRequireUserSession(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/75778270-bdbf-4e2f-bbeb-b3133447a367", strings.NewReader(`{"event":"pull_request"}`))
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"queued"`) ||
		!strings.Contains(response.Body.String(), `"runToken":"run-token"`) || response.Header().Get("Location") == "" {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestPublicAutomationRunUsesScopedTokenWithoutSession(t *testing.T) {
	path := "/api/webhooks/75778270-bdbf-4e2f-bbeb-b3133447a367/runs/96b47a49-96d4-492d-85e6-8c14f0315ae8"
	missing := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, path, nil))
	if missing.Code != http.StatusUnauthorized || !strings.Contains(missing.Body.String(), `"code":"run_token_invalid"`) {
		t.Fatalf("missing token status = %d body = %s", missing.Code, missing.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer run-token")
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestAutomationWebhookRejectsMalformedEndpointID(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/not-a-uuid", strings.NewReader(`{"event":"pull_request"}`))
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestDevelopmentAuthBypassInjectsAdminWithoutCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	response := httptest.NewRecorder()
	debugTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"role":"admin"`) {
		t.Fatalf("debug user is not an admin: %s", response.Body.String())
	}
}

func TestDevelopmentAuthBypassAllowsProtectedEndpointWithoutCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	response := httptest.NewRecorder()
	debugTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestDevelopmentAuthBypassWorksBeforeAdminSetup(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	response := httptest.NewRecorder()
	emptyDebugTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"email":"debug@agentbox.local"`) {
		t.Fatalf("missing fallback debug user: %s", response.Body.String())
	}
}

func TestLoginCreatesHTTPOnlySession(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"password123"}`))
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("session cookie = %#v", cookies)
	}
}

func TestLoginDoesNotAcceptEmailAsIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@agentbox.local","password":"password123"}`))
	response := httptest.NewRecorder()
	rawTestHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCurrentUserUpdatePreservesRoleAndStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/auth/me", strings.NewReader(`{"name":"Updated Admin","username":"updated-admin","email":"updated@example.com","password":"","role":"viewer","status":"disabled"}`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"role":"admin"`) || !strings.Contains(response.Body.String(), `"status":"active"`) {
		t.Fatalf("self update changed protected fields: %s", response.Body.String())
	}
}

func TestCurrentUserPreferencesUpdate(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/auth/preferences", strings.NewReader(`{"successNotifications":false,"density":"compact","showCapabilities":true,"showInfrastructure":false,"showGovernance":true}`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"density":"compact"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestPullCredentialModelsEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/credentials/openai-primary/models/pull", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"gpt-5"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestAddCredentialModelEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/credentials/openai-primary/models", strings.NewReader(`{"id":"custom-model","name":"Custom Model"}`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"source":"manual"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestDeleteCredentialModelEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/api/credentials/openai-primary/models?modelId=custom-model", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"models":[]`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyAgentRoutesAreRemoved(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestCreateRejectsUnknownJSONFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCreateServerPairing(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/server-pairings", nil)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"token"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestWorkerClaimReturnsNoContentWhenQueueIsEmpty(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/jobs/claim",
		nil,
	)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestSandboxCodexLoginActionIsRemoved(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/sandboxes/sandbox-one/actions/login-codex",
		nil,
	)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestSandboxAgentToolActionAcceptsSelectedTools(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/sandboxes/sandbox-one/agent-tools/actions/update",
		strings.NewReader(`{"tools":["codex","claude-code"]}`),
	)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted ||
		!strings.Contains(response.Body.String(), `"requestedAgentTools":["codex","claude-code"]`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestSandboxNetworkProxyUpdateAcceptsDirectAndNamedProxy(t *testing.T) {
	for name, body := range map[string]string{
		"direct": `{"proxyId":""}`,
		"proxy":  `{"proxyId":"office-proxy"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPatch,
				"/api/sandboxes/sandbox-one/network-proxy",
				strings.NewReader(body),
			)
			response := httptest.NewRecorder()
			testHandler().ServeHTTP(response, request)
			if response.Code != http.StatusAccepted ||
				!strings.Contains(response.Body.String(), strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")) {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLegacyQueuedWorkspaceRoutesAreRemoved(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/sandboxes/sandbox-one/workspace/operations",
		strings.NewReader(`{"kind":"exec","command":"pwd"}`),
	)
	response := httptest.NewRecorder()
	testHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
