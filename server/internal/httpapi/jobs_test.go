package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
