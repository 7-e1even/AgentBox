package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestNormalizeWorkerJobResultAcceptsBoundedStructuredError(t *testing.T) {
	result := platform.WorkerJobResult{Error: &platform.WorkerJobError{
		Code: "sandbox_create_failed", Stage: "image-prepare", Retryable: true,
		Details: map[string]string{"action": "create-sandbox"},
	}}
	if !normalizeWorkerJobResult(&result) {
		t.Fatal("valid structured Worker error was rejected")
	}
	if result.Error.Code != "sandbox_create_failed" || result.Error.Stage != "image-prepare" {
		t.Fatalf("normalized Worker error = %#v", result.Error)
	}
}

func TestNormalizeWorkerJobResultRejectsUnsafeErrorMetadata(t *testing.T) {
	for _, workerError := range []*platform.WorkerJobError{
		{Code: "Sandbox Failed"},
		{Code: "sandbox_failed", Stage: "../../secret"},
		{Code: "sandbox_failed", Details: map[string]string{"bad key": "value"}},
		{Code: "sandbox_failed", Details: map[string]string{"api_key": "secret"}},
	} {
		result := platform.WorkerJobResult{Error: workerError}
		if normalizeWorkerJobResult(&result) {
			t.Fatalf("unsafe Worker error was accepted: %#v", workerError)
		}
	}
}

func TestWorkerJobEndpointsRejectNegativeLeaseGeneration(t *testing.T) {
	for name, test := range map[string]struct {
		path string
		body string
	}{
		"progress": {
			path: "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/jobs/754f76dd-2297-44e9-8204-a688be9be4a5/progress",
			body: `{"leaseGeneration":-1,"stage":"runtime","message":"running"}`,
		},
		"complete": {
			path: "/api/servers/7b20f83b-6418-4a9f-8477-3dc7c35d6310/jobs/754f76dd-2297-44e9-8204-a688be9be4a5/complete",
			body: `{"leaseGeneration":-1,"success":true,"message":"done"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			testHandler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body = %s)", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestNormalizeWorkerJobResultAcceptsAgentToolStates(t *testing.T) {
	result := platform.WorkerJobResult{
		Success: true,
		AgentTools: []platform.SandboxAgentToolState{{
			Tool: "codex", CurrentVersion: " 0.1.0 ", LatestVersion: "0.2.0",
			Status: "updated", Source: "npm", CheckedAt: time.Now().UTC(),
		}},
	}
	if !normalizeWorkerJobResult(&result) {
		t.Fatal("valid Agent tool states were rejected")
	}
	if result.AgentTools[0].CurrentVersion != "0.1.0" {
		t.Fatalf("current version was not normalized: %q", result.AgentTools[0].CurrentVersion)
	}
}

func TestNormalizeWorkerJobResultRejectsUnknownOrDuplicateAgentTools(t *testing.T) {
	for _, tools := range [][]platform.SandboxAgentToolState{
		{{Tool: "unknown", Status: "installed"}},
		{{Tool: "codex", Status: "installed"}, {Tool: "codex", Status: "updated"}},
		{{Tool: "codex", Status: "pretend-updated"}},
	} {
		result := platform.WorkerJobResult{Success: true, AgentTools: tools}
		if normalizeWorkerJobResult(&result) {
			t.Fatalf("unsafe Agent tool states were accepted: %#v", tools)
		}
	}
}

func TestWorkerRequestBaseURLUsesForwardedPublicAddress(t *testing.T) {
	request := httptest.NewRequest("POST", "http://127.0.0.1:8091/api/servers/id/jobs/claim", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "agentbox.example:3000")
	if got := workerRequestBaseURL(request, true); got != "https://agentbox.example:3000" {
		t.Fatalf("workerRequestBaseURL() = %q", got)
	}
}

func TestAttachWorkerRuntimeEndpointsKeepsLegacyWorkersCompatible(t *testing.T) {
	credentials := []map[string]any{
		{"protocol": "anthropic", "facadePath": "/api/runtime/sandboxes/test/llm/kimi"},
		{"protocol": "openai-responses", "facadePath": "/api/runtime/sandboxes/test/llm/openai"},
	}
	payload := map[string]any{"credentials": credentials}
	attachWorkerRuntimeEndpoints(payload, "http://agentbox.local:3000")

	if got := credentials[0]["endpoint"]; got != "http://agentbox.local:3000/api/runtime/sandboxes/test/llm/kimi/anthropic" {
		t.Fatalf("Anthropic endpoint = %#v", got)
	}
	if got := credentials[1]["endpoint"]; got != "http://agentbox.local:3000/api/runtime/sandboxes/test/llm/openai/openai/v1" {
		t.Fatalf("Responses endpoint = %#v", got)
	}
	controlPlane := payload["controlPlane"].(map[string]any)
	allowNet := controlPlane["allowNet"].([]string)
	if strings.Join(allowNet, ",") != "agentbox.local" {
		t.Fatalf("control plane allowNet = %#v", allowNet)
	}
}

func TestAttachWorkerRuntimeEndpointsAllowsControlPlaneWithoutProxy(t *testing.T) {
	payload := map[string]any{}
	attachWorkerRuntimeEndpoints(payload, "http://192.168.31.83:8091")

	controlPlane := payload["controlPlane"].(map[string]any)
	allowNet := controlPlane["allowNet"].([]string)
	if strings.Join(allowNet, ",") != "192.168.31.83" {
		t.Fatalf("control plane allowNet = %#v", allowNet)
	}
	if _, exists := payload["proxy"]; exists {
		t.Fatal("control plane allowlist must not require a network proxy")
	}
}

func TestAttachWorkerRuntimeEndpointsKeepsLoopbackAllowlistNonEmpty(t *testing.T) {
	credentials := []map[string]any{
		{"protocol": "anthropic", "facadePath": "/api/runtime/sandboxes/test/llm/kimi"},
	}
	payload := map[string]any{"driver": "boxlite", "credentials": credentials}
	attachWorkerRuntimeEndpoints(payload, "http://127.0.0.1:8091")

	if got := credentials[0]["endpoint"]; got != "http://127.0.0.1:8091/api/runtime/sandboxes/test/llm/kimi/anthropic" {
		t.Fatalf("Anthropic endpoint = %#v", got)
	}
	controlPlane := payload["controlPlane"].(map[string]any)
	allowNet := controlPlane["allowNet"].([]string)
	if strings.Join(allowNet, ",") != "127.0.0.1" {
		t.Fatalf("control plane allowNet = %#v", allowNet)
	}
}

func TestAttachWorkerProxyEndpoint(t *testing.T) {
	payload := map[string]any{"proxy": map[string]any{
		"host":    "proxy.internal",
		"noProxy": []string{"registry.internal", "localhost"},
	}}
	attachWorkerProxyEndpoint(payload, "http://192.168.31.83:8091")
	proxy := payload["proxy"].(map[string]any)
	noProxy := proxy["noProxy"].([]string)
	if strings.Join(noProxy, ",") != "localhost,127.0.0.1,::1,registry.internal,192.168.31.83" {
		t.Fatalf("noProxy = %#v", noProxy)
	}
	allowNet := proxy["allowNet"].([]string)
	if strings.Join(allowNet, ",") != "proxy.internal,registry.internal,192.168.31.83" {
		t.Fatalf("allowNet = %#v", allowNet)
	}
}
