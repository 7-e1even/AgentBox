package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type runtimeLLMTestStore struct {
	fakeStore
	target    platform.RuntimeLLMTarget
	wantToken string
}

type runtimeLLMCountingStore struct {
	runtimeLLMTestStore
	validateCalls int
	resolveCalls  int
}

func (s *runtimeLLMCountingStore) ValidateRuntimeLLMToken(sandboxID, credentialID, token string) error {
	s.validateCalls++
	return s.runtimeLLMTestStore.ValidateRuntimeLLMToken(sandboxID, credentialID, token)
}

func (s *runtimeLLMCountingStore) ResolveRuntimeLLMTarget(
	ctx context.Context, sandboxID, credentialID, token string,
) (platform.RuntimeLLMTarget, error) {
	s.resolveCalls++
	return s.runtimeLLMTestStore.ResolveRuntimeLLMTarget(ctx, sandboxID, credentialID, token)
}

type runtimeLLMHotSwitchStore struct {
	fakeStore
	mu     sync.RWMutex
	slotID string
	token  string
	target platform.RuntimeLLMTarget
	next   platform.RuntimeLLMTarget
}

func (s *runtimeLLMHotSwitchStore) ValidateRuntimeLLMToken(sandboxID, credentialID, token string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sandboxID != s.target.SandboxID || credentialID != s.slotID || token != s.token {
		return store.ErrRuntimeUnauthorized
	}
	return nil
}

func (s *runtimeLLMHotSwitchStore) ResolveRuntimeLLMTarget(
	_ context.Context, sandboxID, credentialID, token string,
) (platform.RuntimeLLMTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sandboxID != s.target.SandboxID || credentialID != s.slotID || token != s.token {
		return platform.RuntimeLLMTarget{}, store.ErrRuntimeUnauthorized
	}
	return s.target, nil
}

func (s *runtimeLLMHotSwitchStore) UpdateSandboxModelSource(
	_ context.Context, id string, input platform.SandboxModelSourceInput,
) (platform.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.target.SandboxID || input.SlotCredentialID != s.slotID ||
		input.CredentialID != s.next.CredentialID || input.ModelID != s.next.ModelID ||
		input.ExpectedCredentialID != s.target.CredentialID || input.ExpectedModelID != s.target.ModelID {
		return platform.Resource{}, &platform.ValidationError{Message: "invalid test model source"}
	}
	s.target = s.next
	return platform.Resource{Input: platform.Input{
		ID: id, Kind: platform.KindSandbox, Name: "hot switch",
		Spec: map[string]any{"runtimeModelSources": map[string]any{
			s.slotID: map[string]any{"credentialId": input.CredentialID, "modelId": input.ModelID},
		}},
	}}, nil
}

func (s runtimeLLMTestStore) ResolveRuntimeLLMTarget(
	_ context.Context, sandboxID, credentialID, token string,
) (platform.RuntimeLLMTarget, error) {
	if token != s.wantToken || sandboxID != s.target.SandboxID || credentialID != s.target.CredentialID {
		return platform.RuntimeLLMTarget{}, store.ErrRuntimeUnauthorized
	}
	return s.target, nil
}

func (s runtimeLLMTestStore) ValidateRuntimeLLMToken(sandboxID, credentialID, token string) error {
	if token != s.wantToken || sandboxID != s.target.SandboxID || credentialID != s.target.CredentialID {
		return store.ErrRuntimeUnauthorized
	}
	return nil
}

func runtimeLLMHandler(t *testing.T, target platform.RuntimeLLMTarget) http.Handler {
	t.Helper()
	return runtimeLLMTestServer(t, runtimeLLMTestStore{target: target, wantToken: "sandbox-token"})
}

func runtimeLLMTestServer(t *testing.T, repository PlatformStore) *Server {
	t.Helper()
	t.Setenv("AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS", "true")
	server := New(
		repository,
		catalog.BuiltinCatalog,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		Config{DisableAuth: true},
	)
	server.runtimeLLMClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Test servers use an ephemeral self-signed certificate.
		InsecureSkipVerify: true, //nolint:gosec
	}}}
	return server
}

func TestRuntimeLLMHotSwitchKeepsInflightTargetAndRoutesLaterRequestsToNewSource(t *testing.T) {
	t.Setenv("AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS", "true")
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	firstModel := make(chan string, 1)
	secondModel := make(chan string, 1)
	var releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(func() { close(firstRelease) }) }
	defer releaseFirst()

	writeChatResponse := func(w http.ResponseWriter, model, content string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chat","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, model, content)
	}
	firstUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode first upstream request: %v", err)
		}
		firstModel <- fmt.Sprint(payload["model"])
		close(firstStarted)
		<-firstRelease
		writeChatResponse(w, "model-a", "source-a")
	}))
	defer firstUpstream.Close()
	secondUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode second upstream request: %v", err)
		}
		secondModel <- fmt.Sprint(payload["model"])
		writeChatResponse(w, "model-b", "source-b")
	}))
	defer secondUpstream.Close()

	storage := &runtimeLLMHotSwitchStore{
		slotID: "slot-one", token: "sandbox-token",
		target: platform.RuntimeLLMTarget{
			SandboxID: "sandbox-one", CredentialID: "source-a", ProviderID: "openai",
			Protocol: "openai-chat", Endpoint: firstUpstream.URL + "/v1", ModelID: "model-a", Secret: "secret-a",
		},
		next: platform.RuntimeLLMTarget{
			SandboxID: "sandbox-one", CredentialID: "source-b", ProviderID: "openai",
			Protocol: "openai-chat", Endpoint: secondUpstream.URL + "/v1", ModelID: "model-b", Secret: "secret-b",
		},
	}
	handler := runtimeLLMTestServer(t, storage)
	requestForRuntime := func() *http.Request {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/runtime/sandboxes/sandbox-one/llm/slot-one/openai/v1/chat/completions",
			strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`),
		)
		request.Header.Set("Authorization", "Bearer sandbox-token")
		return request
	}

	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(firstResponse, requestForRuntime())
		close(firstDone)
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		releaseFirst()
		t.Fatal("first request did not reach its original upstream")
	}

	switchResponse := httptest.NewRecorder()
	switchRequest := httptest.NewRequest(
		http.MethodPatch,
		"/api/sandboxes/sandbox-one/model-source",
		strings.NewReader(`{"slotCredentialId":"slot-one","credentialId":"source-b","modelId":"model-b","expectedCredentialId":"source-a","expectedModelId":"model-a"}`),
	)
	switchRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(switchResponse, switchRequest)
	if switchResponse.Code != http.StatusOK {
		releaseFirst()
		t.Fatalf("switch status = %d body = %s", switchResponse.Code, switchResponse.Body.String())
	}

	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, requestForRuntime())
	if secondResponse.Code != http.StatusOK || !strings.Contains(secondResponse.Body.String(), "source-b") {
		releaseFirst()
		t.Fatalf("second response status = %d body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	releaseFirst()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not finish after its upstream was released")
	}
	if firstResponse.Code != http.StatusOK || !strings.Contains(firstResponse.Body.String(), "source-a") {
		t.Fatalf("first response status = %d body = %s", firstResponse.Code, firstResponse.Body.String())
	}
	if got := <-firstModel; got != "model-a" {
		t.Fatalf("first upstream model = %q", got)
	}
	if got := <-secondModel; got != "model-b" {
		t.Fatalf("second upstream model = %q", got)
	}
}

func TestRuntimeLLMGeminiURLRewritesBoundModelAndPreservesOperationAndQuery(t *testing.T) {
	target := platform.RuntimeLLMTarget{
		Protocol: "gemini", Endpoint: "https://generativelanguage.example.test/gateway/v1beta",
		ModelID: "team/model:preview ?", Secret: "upstream&secret",
	}
	upstream, err := runtimeLLMGeminiURL(
		target,
		"v1beta/models/old-model:streamGenerateContent",
		url.Values{"alt": {"sse"}, "trace": {"one", "two"}, "key": {"untrusted"}},
	)
	if err != nil {
		t.Fatalf("runtimeLLMGeminiURL() error = %v", err)
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse Gemini upstream URL: %v", err)
	}
	if got, want := parsed.EscapedPath(), "/gateway/v1beta/models/team%2Fmodel%3Apreview%20%3F:streamGenerateContent"; got != want {
		t.Fatalf("escaped path = %q, want %q", got, want)
	}
	if strings.Contains(parsed.EscapedPath(), "old-model") {
		t.Fatalf("escaped path retained client model: %q", parsed.EscapedPath())
	}
	if parsed.Query().Get("key") != target.Secret || parsed.Query().Get("alt") != "sse" ||
		len(parsed.Query()["trace"]) != 2 {
		t.Fatalf("forwarded query = %#v", parsed.Query())
	}
}

func TestRuntimeLLMGeminiURLUsesEndpointVersionWhenClientVersionDiffers(t *testing.T) {
	for _, test := range []struct {
		name        string
		endpoint    string
		requestPath string
		wantPath    string
	}{
		{
			name:        "v1 endpoint with v1beta client path",
			endpoint:    "https://generativelanguage.example.test/gateway/v1",
			requestPath: "v1beta/models/client-model:generateContent",
			wantPath:    "/gateway/v1/models/bound-model:generateContent",
		},
		{
			name:        "v1beta endpoint with v1 client path",
			endpoint:    "https://generativelanguage.example.test/gateway/v1beta",
			requestPath: "v1/models/client-model:streamGenerateContent",
			wantPath:    "/gateway/v1beta/models/bound-model:streamGenerateContent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream, err := runtimeLLMGeminiURL(platform.RuntimeLLMTarget{
				Protocol: "gemini", Endpoint: test.endpoint, ModelID: "bound-model", Secret: "secret",
			}, test.requestPath, nil)
			if err != nil {
				t.Fatalf("runtimeLLMGeminiURL() error = %v", err)
			}
			parsed, err := url.Parse(upstream)
			if err != nil {
				t.Fatalf("parse Gemini upstream URL: %v", err)
			}
			if parsed.Path != test.wantPath {
				t.Fatalf("path = %q, want %q", parsed.Path, test.wantPath)
			}
		})
	}
}

func TestRuntimeLLMGeminiURLRejectsInvalidModelPaths(t *testing.T) {
	target := platform.RuntimeLLMTarget{
		Protocol: "gemini", Endpoint: "https://generativelanguage.example.test/v1beta",
		ModelID: "gemini-bound", Secret: "secret",
	}
	for _, requestPath := range []string{
		"v1beta/files/file-one",
		"v1beta/models/model-without-operation",
		"v1beta/models/:generateContent",
		"v1beta/models/client:generateContent/extra",
		"v2/models/client:generateContent",
	} {
		t.Run(requestPath, func(t *testing.T) {
			if _, err := runtimeLLMGeminiURL(target, requestPath, nil); err == nil {
				t.Fatal("runtimeLLMGeminiURL() accepted an invalid model path")
			}
		})
	}
}

func TestRuntimeLLMURLsRejectCleartextProviderEndpoints(t *testing.T) {
	openAI := platform.RuntimeLLMTarget{
		Protocol: "openai-responses", Endpoint: "http://api.example.test/v1", ModelID: "model", Secret: "secret",
	}
	if _, err := runtimeLLMUpstreamURL(openAI, false); err == nil {
		t.Fatal("runtimeLLMUpstreamURL() accepted a cleartext endpoint")
	}
	gemini := platform.RuntimeLLMTarget{
		Protocol: "gemini", Endpoint: "http://api.example.test/v1beta", ModelID: "model", Secret: "secret",
	}
	if _, err := runtimeLLMGeminiGenerateURL(gemini, false); err == nil {
		t.Fatal("runtimeLLMGeminiGenerateURL() accepted a cleartext endpoint")
	}
	if _, err := runtimeLLMGeminiURL(gemini, "models/client:generateContent", nil); err == nil {
		t.Fatal("runtimeLLMGeminiURL() accepted a cleartext endpoint")
	}
}

func TestRuntimeLLMTimeoutsAreFinite(t *testing.T) {
	if got := runtimeLLMTimeout(false); got != 2*time.Minute {
		t.Fatalf("non-streaming timeout = %v", got)
	}
	if got := runtimeLLMTimeout(true); got != 3*time.Minute {
		t.Fatalf("streaming timeout = %v", got)
	}
}

func TestRuntimeLLMAdmissionRejectionUsesProtocol429(t *testing.T) {
	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-responses", Endpoint: "https://api.example.test/v1", ModelID: "model",
		Secret: "upstream-secret",
	}
	storage := &runtimeLLMCountingStore{runtimeLLMTestStore: runtimeLLMTestStore{
		target: target, wantToken: "sandbox-token",
	}}
	server := runtimeLLMTestServer(t, storage)
	server.runtimeLLMAdmission = newRuntimeLLMAdmission(runtimeLLMAdmissionLimits{
		resolutionConcurrency: 0, globalConcurrency: 1, sandboxConcurrency: 1,
		globalRate: 1, sandboxRate: 1, maxSandboxes: 1, window: time.Minute,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/openai/v1/responses",
		strings.NewReader(`{"model":"ignored","input":"hello"}`),
	)
	request.Header.Set("Authorization", "Bearer sandbox-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("status = %d, Retry-After = %q, body = %s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	if storage.resolveCalls != 0 {
		t.Fatalf("ResolveRuntimeLLMTarget calls = %d, want 0 before resolution admission", storage.resolveCalls)
	}
	if storage.validateCalls != 1 {
		t.Fatalf("ValidateRuntimeLLMToken calls = %d, want 1 before admission", storage.validateCalls)
	}
	if got := len(server.runtimeLLMAdmission.sandboxes); got != 0 {
		t.Fatalf("unauthenticated sandbox admission keys = %d, want 0", got)
	}
}

func TestRuntimeLLMSandboxAdmissionRunsAfterTargetResolution(t *testing.T) {
	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-responses", Endpoint: "https://api.example.test/v1", ModelID: "model",
		Secret: "upstream-secret",
	}
	storage := &runtimeLLMCountingStore{runtimeLLMTestStore: runtimeLLMTestStore{
		target: target, wantToken: "sandbox-token",
	}}
	server := runtimeLLMTestServer(t, storage)
	server.runtimeLLMAdmission = newRuntimeLLMAdmission(runtimeLLMAdmissionLimits{
		resolutionConcurrency: 1, globalConcurrency: 1, sandboxConcurrency: 0,
		globalRate: 10, sandboxRate: 1, maxSandboxes: 1, window: time.Minute,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/openai/v1/responses",
		strings.NewReader(`{"model":"ignored","input":"hello"}`),
	)
	request.Header.Set("Authorization", "Bearer sandbox-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if storage.resolveCalls != 1 {
		t.Fatalf("ResolveRuntimeLLMTarget calls = %d, want 1 before sandbox admission", storage.resolveCalls)
	}
	if storage.validateCalls != 1 {
		t.Fatalf("ValidateRuntimeLLMToken calls = %d, want 1", storage.validateCalls)
	}
	if server.runtimeLLMAdmission.resolutionActive != 0 {
		t.Fatalf("active resolutions = %d after target rejection, want 0", server.runtimeLLMAdmission.resolutionActive)
	}
	if server.runtimeLLMAdmission.globalActive != 0 {
		t.Fatalf("global active requests = %d after rejection, want 0", server.runtimeLLMAdmission.globalActive)
	}
}

func TestRuntimeLLMInvalidTokenCannotCreateAdmissionState(t *testing.T) {
	target := platform.RuntimeLLMTarget{SandboxID: "sandbox-one", CredentialID: "credential-one"}
	storage := &runtimeLLMCountingStore{runtimeLLMTestStore: runtimeLLMTestStore{
		target: target, wantToken: "sandbox-token",
	}}
	server := runtimeLLMTestServer(t, storage)
	server.runtimeLLMAdmission = newRuntimeLLMAdmission(runtimeLLMAdmissionLimits{
		resolutionConcurrency: 1, globalConcurrency: 1, sandboxConcurrency: 1,
		globalRate: 1, sandboxRate: 1, maxSandboxes: 1, window: time.Minute,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/attacker-chosen/llm/attacker-chosen/openai/v1/responses",
		strings.NewReader(`{"model":"ignored","input":"hello"}`),
	)
	request.Header.Set("Authorization", "Bearer invalid-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if storage.validateCalls != 1 || storage.resolveCalls != 0 {
		t.Fatalf("validate calls = %d, resolve calls = %d", storage.validateCalls, storage.resolveCalls)
	}
	if got := len(server.runtimeLLMAdmission.sandboxes); got != 0 {
		t.Fatalf("invalid token created %d sandbox admission keys", got)
	}
	if len(server.runtimeLLMAdmission.resolutionStarts) != 0 {
		t.Fatal("invalid token consumed authenticated resolution rate capacity")
	}
}

func TestRuntimeLLMConvertsAnthropicToResponses(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("path = %s, want /v1/responses", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Errorf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["model"] != "gpt-bound" {
			t.Errorf("model = %#v, want gpt-bound", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
          "id":"resp_1","object":"response","created_at":1700000000,
          "model":"gpt-bound","status":"completed",
          "output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant",
            "content":[{"type":"output_text","text":"hello","annotations":[]}]}],
          "usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}
        }`)
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-responses", Endpoint: upstream.URL + "/v1", ModelID: "gpt-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-AgentBox-Conversion"); got != "anthropic->openai-responses" {
		t.Fatalf("conversion header = %q", got)
	}
	if !strings.Contains(response.Body.String(), `"type":"message"`) ||
		!strings.Contains(response.Body.String(), `"text":"hello"`) {
		t.Fatalf("unexpected Anthropic response: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsAnthropicToGeminiEndToEnd(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/gemini-bound:generateContent" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if got := request.Header.Get("x-goog-api-key"); got != "upstream-secret" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if !strings.Contains(compactJSON(payload["contents"]), "hello Gemini") {
			t.Errorf("converted contents = %#v", payload["contents"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
          "candidates":[{"content":{"role":"model","parts":[{"text":"hello Claude"}]},"finishReason":"STOP","index":0}],
          "usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},
          "modelVersion":"gemini-bound","responseId":"gemini_1"
        }`)
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "google",
		Protocol: "gemini", Endpoint: upstream.URL + "/v1beta", ModelID: "gemini-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":64,"messages":[{"role":"user","content":"hello Gemini"}]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-AgentBox-Conversion-Engine") != "CLIProxyAPI/v6.7.53" {
		t.Fatalf("conversion engine = %q", response.Header().Get("X-AgentBox-Conversion-Engine"))
	}
	if !strings.Contains(response.Body.String(), `"text":"hello Claude"`) {
		t.Fatalf("unexpected Anthropic response: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsGeminiPDFToAnthropicEndToEnd(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if got := request.Header.Get("x-api-key"); got != "upstream-secret" {
			t.Errorf("x-api-key = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if countAnthropicContentType(payload, "document") != 1 {
			t.Errorf("PDF document was lost: %s", compactJSON(payload))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
          "id":"msg_1","type":"message","role":"assistant","model":"claude-bound",
          "content":[{"type":"text","text":"PDF received"}],"stop_reason":"end_turn",
          "usage":{"input_tokens":2,"output_tokens":2}
        }`)
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: upstream.URL + "/v1", ModelID: "claude-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/gemini/v1beta/models/client-model:generateContent?key=sandbox-token",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"inspect"},{"inlineData":{"mimeType":"application/pdf","data":"cGRm"}}]}]}`),
	)
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"text":"PDF received"`) ||
		!strings.Contains(response.Body.String(), `"candidates"`) {
		t.Fatalf("unexpected Gemini response: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsResponsesToAnthropicWithReasoningAndTools(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			t.Errorf("path = %s, want /v1/messages", request.URL.Path)
		}
		if got := request.Header.Get("x-api-key"); got != "upstream-secret" {
			t.Errorf("x-api-key = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["model"] != "claude-bound" {
			t.Errorf("model = %#v, want claude-bound", payload["model"])
		}
		thinking, _ := payload["thinking"].(map[string]any)
		if thinking["type"] != "enabled" {
			t.Errorf("thinking = %#v", payload["thinking"])
		}
		tools, _ := payload["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("tools = %#v", payload["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
          "id":"msg_1","type":"message","role":"assistant","model":"claude-bound",
          "content":[{"type":"text","text":"done"}],"stop_reason":"end_turn",
          "usage":{"input_tokens":8,"output_tokens":3}
        }`)
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: upstream.URL + "/v1", ModelID: "claude-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/openai/v1/responses",
		strings.NewReader(`{
          "model":"ignored","input":[{"type":"message","role":"user",
            "content":[{"type":"input_text","text":"hi"}]}],
          "reasoning":{"effort":"high"},
          "tools":[{"type":"function","name":"shell","description":"run a command",
            "parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]
        }`),
	)
	request.Header.Set("Authorization", "Bearer sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"object":"response"`) ||
		!strings.Contains(response.Body.String(), `"text":"done"`) {
		t.Fatalf("unexpected Responses response: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsResponsesStreamToValidAnthropicLifecycle(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["stream"] != true {
			t.Errorf("stream = %#v, want true", payload["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_stream","model":"gpt-bound"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"hello"}`,
			`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hello"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`{"type":"response.completed","response":{"id":"resp_stream","model":"gpt-bound","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-responses", Endpoint: upstream.URL + "/v1", ModelID: "gpt-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	assertAnthropicStreamLifecycle(t, response.Body.String(), 0)
	if !strings.Contains(response.Body.String(), `"text":"hello"`) {
		t.Fatalf("stream does not contain text delta: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsParallelResponsesToolsToValidAnthropicLifecycle(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_parallel","model":"gpt-bound"}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_a","name":"Read"}}`,
			`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"call_b","name":"Read"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"file_path\":\"a\"}"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{\"file_path\":\"a\"}"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"file_path\":\"b\"}"}`,
			`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","call_id":"call_b","name":"Read","arguments":"{\"file_path\":\"b\"}"}}`,
			`{"type":"response.completed","response":{"id":"resp_parallel","model":"gpt-bound","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-responses", Endpoint: upstream.URL + "/v1", ModelID: "gpt-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"read"}]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	assertAnthropicStreamLifecycle(t, response.Body.String(), 2)
	if !strings.Contains(response.Body.String(), `"id":"call_a"`) ||
		!strings.Contains(response.Body.String(), `"id":"call_b"`) {
		t.Fatalf("parallel tool IDs were not preserved: %s", response.Body.String())
	}
}

func TestRuntimeLLMConvertsParallelChatToolsToValidAnthropicLifecycle(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"id":"chat_parallel","model":"chat-bound","choices":[{"index":0,"delta":{"role":"assistant","content":"checking"},"finish_reason":null}]}`,
			`{"id":"chat_parallel","model":"chat-bound","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"Read","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chat_parallel","model":"chat-bound","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":\"a\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chat_parallel","model":"chat-bound","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"b\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chat_parallel","model":"chat-bound","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"id":"chat_parallel","model":"chat-bound","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "openai",
		Protocol: "openai-chat", Endpoint: upstream.URL + "/v1", ModelID: "chat-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"read"}]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	assertAnthropicStreamLifecycle(t, response.Body.String(), 2)
	for _, value := range []string{`"text":"checking"`, `"id":"call_a"`, `"id":"call_b"`} {
		if !strings.Contains(response.Body.String(), value) {
			t.Fatalf("converted stream does not contain %s: %s", value, response.Body.String())
		}
	}
}

func TestRuntimeLLMConvertsAnthropicStreamToResponses(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"claude-bound","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "event: test\ndata: %s\n\n", event)
		}
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: upstream.URL + "/v1", ModelID: "claude-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/openai/v1/responses",
		strings.NewReader(`{"model":"ignored","stream":true,"input":"hi"}`),
	)
	request.Header.Set("Authorization", "Bearer sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	assertResponsesStreamLifecycle(t, response.Body.String())
	for _, value := range []string{`"type":"response.created"`, `"type":"response.output_text.delta"`, `"delta":"hello"`, `"type":"response.completed"`} {
		if !strings.Contains(response.Body.String(), value) {
			t.Fatalf("converted stream does not contain %s: %s", value, response.Body.String())
		}
	}
}

func TestRuntimeLLMConvertsAnthropicToolStreamToResponses(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-bound","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_read","name":"container_exec","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"README.md\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "event: test\ndata: %s\n\n", event)
		}
	}))
	defer upstream.Close()

	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: upstream.URL + "/v1", ModelID: "claude-bound",
		Secret: "upstream-secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/openai/v1/responses",
		strings.NewReader(`{
          "model":"ignored","stream":true,"input":"read",
          "tools":[{"type":"function","name":"container.exec","description":"run",
            "parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]
        }`),
	)
	request.Header.Set("Authorization", "Bearer sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)

	assertResponsesStreamLifecycle(t, response.Body.String())
	for _, value := range []string{
		`"type":"function_call"`, `"call_id":"call_read"`,
		`"name":"container.exec"`,
		`"arguments":"{\"file_path\":\"README.md\"}"`,
	} {
		if !strings.Contains(response.Body.String(), value) {
			t.Fatalf("converted tool stream does not contain %s: %s", value, response.Body.String())
		}
	}
}

func assertResponsesStreamLifecycle(t *testing.T, body string) {
	t.Helper()
	created := false
	completed := false
	activeItems := make(map[int]bool)
	activeParts := make(map[int]bool)
	for line := range strings.SplitSeq(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			continue
		}
		var event struct {
			Type        string `json:"type"`
			OutputIndex int    `json:"output_index"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("invalid Responses SSE JSON %q: %v", line, err)
		}
		if completed {
			t.Fatalf("event emitted after response.completed: %s", body)
		}
		switch event.Type {
		case "response.created":
			if created {
				t.Fatalf("duplicate response.created: %s", body)
			}
			created = true
		case "response.output_item.added":
			if !created || activeItems[event.OutputIndex] {
				t.Fatalf("output item added out of order: %s", body)
			}
			activeItems[event.OutputIndex] = true
		case "response.content_part.added":
			if !activeItems[event.OutputIndex] || activeParts[event.OutputIndex] {
				t.Fatalf("content part added out of order: %s", body)
			}
			activeParts[event.OutputIndex] = true
		case "response.output_text.delta":
			if !activeItems[event.OutputIndex] || !activeParts[event.OutputIndex] {
				t.Fatalf("text delta without active item and content part: %s", body)
			}
		case "response.content_part.done":
			if !activeParts[event.OutputIndex] {
				t.Fatalf("content part done without active part: %s", body)
			}
			delete(activeParts, event.OutputIndex)
		case "response.output_item.done":
			if !activeItems[event.OutputIndex] || activeParts[event.OutputIndex] {
				t.Fatalf("output item done out of order: %s", body)
			}
			delete(activeItems, event.OutputIndex)
		case "response.completed":
			if !created || len(activeItems) != 0 || len(activeParts) != 0 {
				t.Fatalf("response completed with open items: %s", body)
			}
			completed = true
		}
	}
	if !created || !completed || len(activeItems) != 0 || len(activeParts) != 0 {
		t.Fatalf("incomplete Responses stream lifecycle: %s", body)
	}
}

func assertAnthropicStreamLifecycle(t *testing.T, body string, wantToolBlocks int) {
	t.Helper()
	open := make(map[int]bool)
	toolBlocks := 0
	messageDelta := false
	messageStop := false
	for line := range strings.SplitSeq(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("invalid SSE JSON %q: %v", line, err)
		}
		if messageStop {
			t.Fatalf("event emitted after message_stop: %s", line)
		}
		switch event.Type {
		case "content_block_start":
			if len(open) != 0 || messageDelta {
				t.Fatalf("content block started out of order: %s", body)
			}
			open[event.Index] = true
			if event.ContentBlock.Type == "tool_use" {
				toolBlocks++
			}
		case "content_block_delta":
			if !open[event.Index] {
				t.Fatalf("delta for unopened content block %d: %s", event.Index, body)
			}
		case "content_block_stop":
			if !open[event.Index] {
				t.Fatalf("stop for unopened content block %d: %s", event.Index, body)
			}
			delete(open, event.Index)
		case "message_delta":
			if len(open) != 0 || messageDelta {
				t.Fatalf("message_delta emitted out of order: %s", body)
			}
			messageDelta = true
		case "message_stop":
			if !messageDelta || len(open) != 0 {
				t.Fatalf("message_stop emitted out of order: %s", body)
			}
			messageStop = true
		}
	}
	if len(open) != 0 || !messageDelta || !messageStop {
		t.Fatalf("incomplete Anthropic stream lifecycle: %s", body)
	}
	if toolBlocks != wantToolBlocks {
		t.Fatalf("tool blocks = %d, want %d: %s", toolBlocks, wantToolBlocks, body)
	}
}

func TestRuntimeLLMNativeProtocolPreservesExtensionsAndOverridesModel(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload["custom_extension"] != "kept" {
			t.Errorf("custom_extension = %#v", payload["custom_extension"])
		}
		if payload["model"] != "bound-model" {
			t.Errorf("model = %#v", payload["model"])
		}
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"bound-model","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: upstream.URL + "/v1", ModelID: "bound-model", Secret: "secret",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"model":"ignored","max_tokens":64,"custom_extension":"kept","messages":[]}`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRuntimeLLMRejectsMissingSandboxToken(t *testing.T) {
	target := platform.RuntimeLLMTarget{SandboxID: "sandbox-one", CredentialID: "credential-one"}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{}`),
	)
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRuntimeLLMNormalizesCrossProtocolErrors(t *testing.T) {
	tests := []struct {
		name             string
		clientProtocol   runtimeLLMProtocol
		upstreamProtocol string
		path             string
		headers          map[string]string
		upstreamBody     string
		wantBody         []string
	}{
		{
			name:             "Gemini error to Anthropic",
			clientProtocol:   runtimeLLMProtocolAnthropic,
			upstreamProtocol: "gemini",
			path:             "/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
			headers:          map[string]string{"x-api-key": "sandbox-token"},
			upstreamBody:     `{"error":{"code":403,"message":"region denied","status":"PERMISSION_DENIED"}}`,
			wantBody:         []string{`"type":"error"`, `"message":"region denied"`},
		},
		{
			name:             "Anthropic error to Gemini",
			clientProtocol:   runtimeLLMProtocolGemini,
			upstreamProtocol: "anthropic",
			path:             "/api/runtime/sandboxes/sandbox-one/llm/credential-one/gemini/v1beta/models/client:generateContent?key=sandbox-token",
			upstreamBody:     `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
			wantBody:         []string{`"code":403`, `"message":"slow down"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, test.upstreamBody)
			}))
			defer upstream.Close()
			target := platform.RuntimeLLMTarget{
				SandboxID: "sandbox-one", CredentialID: "credential-one",
				Protocol: test.upstreamProtocol, Endpoint: upstream.URL + "/v1",
				ModelID: "bound-model", Secret: "secret",
			}
			body := `{"model":"ignored","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`
			if test.clientProtocol == runtimeLLMProtocolGemini {
				body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(body))
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			runtimeLLMHandler(t, target).ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
			for _, want := range test.wantBody {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("body does not contain %s: %s", want, response.Body.String())
				}
			}
		})
	}
}

func TestRuntimeLLMRejectsMalformedJSON(t *testing.T) {
	target := platform.RuntimeLLMTarget{
		SandboxID: "sandbox-one", CredentialID: "credential-one",
		Protocol: "openai-chat", Endpoint: "http://127.0.0.1:1/v1", ModelID: "bound-model",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runtime/sandboxes/sandbox-one/llm/credential-one/anthropic/v1/messages",
		strings.NewReader(`{"broken":`),
	)
	request.Header.Set("x-api-key", "sandbox-token")
	response := httptest.NewRecorder()
	runtimeLLMHandler(t, target).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}
