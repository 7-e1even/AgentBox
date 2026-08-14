package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestWorkerRequestBaseURLUsesForwardedPublicAddress(t *testing.T) {
	request := httptest.NewRequest("POST", "http://127.0.0.1:8091/api/servers/id/jobs/claim", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "agentbox.example:3000")
	if got := workerRequestBaseURL(request); got != "https://agentbox.example:3000" {
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
}
