package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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

type runtimeLLMTestStore struct {
	fakeStore
	target    platform.RuntimeLLMTarget
	wantToken string
}

func (s runtimeLLMTestStore) ResolveRuntimeLLMTarget(
	_ context.Context, sandboxID, credentialID, token string,
) (platform.RuntimeLLMTarget, error) {
	if token != s.wantToken || sandboxID != s.target.SandboxID || credentialID != s.target.CredentialID {
		return platform.RuntimeLLMTarget{}, store.ErrRuntimeUnauthorized
	}
	return s.target, nil
}

func runtimeLLMHandler(t *testing.T, target platform.RuntimeLLMTarget) http.Handler {
	t.Helper()
	t.Setenv("AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS", "true")
	return New(
		runtimeLLMTestStore{target: target, wantToken: "sandbox-token"},
		catalog.BuiltinCatalog,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		Config{},
	)
}

func TestRuntimeLLMConvertsAnthropicToResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
