package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestRuntimeLLMResponseMatrix(t *testing.T) {
	requests := map[runtimeLLMProtocol]string{
		runtimeLLMProtocolAnthropic: `{"model":"source","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolResponses: `{"model":"source","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
		runtimeLLMProtocolChat:      `{"model":"source","messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolGemini:    `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	responses := map[runtimeLLMProtocol]string{
		runtimeLLMProtocolAnthropic: `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		runtimeLLMProtocolResponses: `{"id":"resp_1","object":"response","created_at":1700000000,"model":"m","status":"completed","output":[{"type":"message","id":"out_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		runtimeLLMProtocolChat:      `{"id":"chat_1","object":"chat.completion","created":1700000000,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		runtimeLLMProtocolGemini:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"m","responseId":"gemini_1"}`,
	}
	protocols := []runtimeLLMProtocol{
		runtimeLLMProtocolAnthropic,
		runtimeLLMProtocolResponses,
		runtimeLLMProtocolChat,
		runtimeLLMProtocolGemini,
	}
	for _, upstream := range protocols {
		for _, client := range protocols {
			if upstream == client {
				continue
			}
			t.Run(string(upstream)+"_to_"+string(client), func(t *testing.T) {
				translatedRequest, err := convertRuntimeLLMRequest(
					client, upstream, "m", []byte(requests[client]), false,
				)
				if err != nil {
					t.Fatalf("convert request: %v", err)
				}
				converted, err := convertRuntimeLLMResponse(
					context.Background(), upstream, client, "m",
					[]byte(requests[client]), translatedRequest, []byte(responses[upstream]),
				)
				if err != nil {
					t.Fatalf("convert response: %v", err)
				}
				if !json.Valid(converted) {
					t.Fatalf("invalid converted JSON: %s", converted)
				}
				if !strings.Contains(string(converted), "hello") {
					t.Fatalf("response text was lost: %s", converted)
				}
			})
		}
	}
}

func TestRuntimeLLMCLIProxyStreamMatrix(t *testing.T) {
	requests := map[runtimeLLMProtocol]string{
		runtimeLLMProtocolAnthropic: `{"model":"source","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolResponses: `{"model":"source","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
		runtimeLLMProtocolChat:      `{"model":"source","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolGemini:    `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	streams := map[runtimeLLMProtocol]string{
		runtimeLLMProtocolAnthropic: strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`data: {"type":"message_stop"}`,
		}, "\n\n") + "\n\n",
		runtimeLLMProtocolResponses: strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_1","model":"m"}}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
			`data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"hello"}`,
			`data: {"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hello"}}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`data: {"type":"response.completed","response":{"id":"resp_1","model":"m","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
			`data: [DONE]`,
		}, "\n\n") + "\n\n",
		runtimeLLMProtocolChat: strings.Join([]string{
			`data: {"id":"chat_1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`,
			`data: {"id":"chat_1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}, "\n\n") + "\n\n",
		runtimeLLMProtocolGemini: strings.Join([]string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"m","responseId":"gemini_1"}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"m","responseId":"gemini_1"}`,
		}, "\n\n") + "\n\n",
	}
	pairs := [][2]runtimeLLMProtocol{
		{runtimeLLMProtocolAnthropic, runtimeLLMProtocolChat},
		{runtimeLLMProtocolAnthropic, runtimeLLMProtocolGemini},
		{runtimeLLMProtocolResponses, runtimeLLMProtocolChat},
		{runtimeLLMProtocolResponses, runtimeLLMProtocolGemini},
		{runtimeLLMProtocolChat, runtimeLLMProtocolResponses},
		{runtimeLLMProtocolChat, runtimeLLMProtocolGemini},
		{runtimeLLMProtocolGemini, runtimeLLMProtocolAnthropic},
		{runtimeLLMProtocolGemini, runtimeLLMProtocolResponses},
		{runtimeLLMProtocolGemini, runtimeLLMProtocolChat},
	}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, pair := range pairs {
		upstream, client := pair[0], pair[1]
		t.Run(string(upstream)+"_to_"+string(client), func(t *testing.T) {
			translatedRequest, err := convertRuntimeLLMRequest(
				client, upstream, "m", []byte(requests[client]), true,
			)
			if err != nil {
				t.Fatalf("convert request: %v", err)
			}
			var output strings.Builder
			err = server.convertRuntimeLLMCLIProxyStream(
				&output, context.Background(), strings.NewReader(streams[upstream]), "m",
				upstream, client, []byte(requests[client]), translatedRequest,
			)
			if err != nil {
				t.Fatalf("convert stream: %v; output=%s", err, output.String())
			}
			if !strings.Contains(output.String(), "hello") {
				t.Fatalf("stream text was lost: %s", output.String())
			}
			assertRuntimeLLMSSEJSON(t, output.String())
		})
	}
}

func assertRuntimeLLMSSEJSON(t *testing.T, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if !json.Valid([]byte(payload)) {
			t.Fatalf("invalid SSE JSON %q in %s", payload, body)
		}
	}
}

func TestRuntimeLLMCLIProxyRequestMatrix(t *testing.T) {
	samples := map[runtimeLLMProtocol]string{
		runtimeLLMProtocolAnthropic: `{"model":"source","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolResponses: `{"model":"source","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
		runtimeLLMProtocolChat:      `{"model":"source","messages":[{"role":"user","content":"hello"}]}`,
		runtimeLLMProtocolGemini:    `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	protocols := []runtimeLLMProtocol{
		runtimeLLMProtocolAnthropic,
		runtimeLLMProtocolResponses,
		runtimeLLMProtocolChat,
		runtimeLLMProtocolGemini,
	}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				converted, err := convertRuntimeLLMRequest(from, to, "target-model", []byte(samples[from]), false)
				if err != nil {
					t.Fatalf("convert request: %v", err)
				}
				if !json.Valid(converted) {
					t.Fatalf("invalid converted JSON: %s", converted)
				}
				if !strings.Contains(string(converted), "target-model") && to != runtimeLLMProtocolGemini {
					t.Fatalf("target model not applied: %s", converted)
				}
			})
		}
	}
}

func TestRuntimeLLMResponsesToAnthropicPreservesImagesDocumentsAndToolResults(t *testing.T) {
	original := []byte(`{
      "model":"source",
      "input":[
        {"type":"message","role":"user","content":[
          {"type":"input_text","text":"inspect both"},
          {"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="},
          {"type":"input_file","filename":"report.pdf","file_data":"data:application/pdf;base64,cGRm"}
        ]},
        {"type":"function_call","call_id":"call_1","name":"inspect","arguments":"{}"},
        {"type":"function_call_output","call_id":"call_1","output":[
          {"type":"input_text","text":"tool text"},
          {"type":"input_image","image_url":"data:image/jpeg;base64,dG9vbC1pbWFnZQ=="},
          {"type":"input_file","filename":"tool.pdf","file_data":"data:application/pdf;base64,dG9vbC1wZGY="}
        ]},
        {"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
      ]
    }`)

	converted, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolResponses, runtimeLLMProtocolAnthropic,
		"claude-target", original, false,
	)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	root := decodeRuntimeLLMJSON(t, converted)
	if countAnthropicContentType(root, "image") < 2 {
		t.Fatalf("images were not preserved: %s", converted)
	}
	if countAnthropicContentType(root, "document") < 2 {
		t.Fatalf("documents were not preserved: %s", converted)
	}
	toolResult := findAnthropicToolResult(root, "call_1")
	if toolResult == nil {
		t.Fatalf("tool result call id was not preserved: %s", converted)
	}
	if got := contentTypes(anySlice(toolResult["content"])); !containsAll(got, "text", "image", "document") {
		t.Fatalf("tool result content types = %v: %s", got, converted)
	}
	if !strings.Contains(string(converted), "continue") {
		t.Fatalf("later turn was lost: %s", converted)
	}
}

func TestRuntimeLLMAnthropicToResponsesPreservesImagesDocumentsAndToolResults(t *testing.T) {
	original := []byte(`{
      "model":"source","max_tokens":64,
      "messages":[
        {"role":"user","content":[
          {"type":"text","text":"inspect"},
          {"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}},
          {"type":"document","source":{"type":"url","url":"https://example.test/report.pdf"}}
        ]},
        {"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"inspect","input":{}}]},
        {"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[
          {"type":"text","text":"tool text"},
          {"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"dG9vbC1pbWFnZQ=="}},
          {"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"dG9vbC1wZGY="}}
        ]}]}
      ]
    }`)

	converted, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolAnthropic, runtimeLLMProtocolResponses,
		"gpt-target", original, false,
	)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	root := decodeRuntimeLLMJSON(t, converted)
	if countResponsesContentType(root, "input_image") < 1 {
		t.Fatalf("image was not preserved: %s", converted)
	}
	if countResponsesContentType(root, "input_file") < 1 {
		t.Fatalf("document was not preserved: %s", converted)
	}
	output := findResponsesToolOutput(root, "call_1")
	if got := contentTypes(anySlice(output)); !containsAll(got, "input_text", "input_image", "input_file") {
		t.Fatalf("tool output content types = %v: %s", got, converted)
	}
	if !strings.Contains(string(converted), "https://example.test/report.pdf") {
		t.Fatalf("document URL was not preserved: %s", converted)
	}
}

func TestRuntimeLLMChatToAnthropicPreservesFilesAndToolResultImages(t *testing.T) {
	original := []byte(`{
      "model":"source",
      "messages":[
        {"role":"user","content":[
          {"type":"text","text":"inspect"},
          {"type":"file","file":{"filename":"report.pdf","file_data":"data:application/pdf;base64,cGRm"}}
        ]},
        {"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"inspect","arguments":"{}"}}]},
        {"role":"tool","tool_call_id":"call_1","content":[
          {"type":"text","text":"tool text"},
          {"type":"image_url","image_url":{"url":"data:image/png;base64,dG9vbC1pbWFnZQ=="}},
          {"type":"file","file":{"filename":"tool.pdf","file_data":"data:application/pdf;base64,dG9vbC1wZGY="}}
        ]}
      ]
    }`)

	converted, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolChat, runtimeLLMProtocolAnthropic,
		"claude-target", original, false,
	)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	root := decodeRuntimeLLMJSON(t, converted)
	if countAnthropicContentType(root, "document") < 2 {
		t.Fatalf("files were not preserved as documents: %s", converted)
	}
	toolResult := findAnthropicToolResult(root, "call_1")
	if toolResult == nil {
		t.Fatalf("tool result was not preserved: %s", converted)
	}
	if got := contentTypes(anySlice(toolResult["content"])); !containsAll(got, "text", "image", "document") {
		t.Fatalf("tool result content types = %v: %s", got, converted)
	}
}

func TestRuntimeLLMCrossProtocolFileIDFailsExplicitly(t *testing.T) {
	tests := []struct {
		name string
		from runtimeLLMProtocol
		to   runtimeLLMProtocol
		body string
	}{
		{
			name: "responses",
			from: runtimeLLMProtocolResponses,
			to:   runtimeLLMProtocolAnthropic,
			body: `{"input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}]}`,
		},
		{
			name: "chat",
			from: runtimeLLMProtocolChat,
			to:   runtimeLLMProtocolAnthropic,
			body: `{"messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file_123"}}]}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := convertRuntimeLLMRequest(test.from, test.to, "target", []byte(test.body), false)
			if err == nil || !strings.Contains(err.Error(), "file_id") {
				t.Fatalf("error = %v, want explicit file_id error", err)
			}
		})
	}
}

func TestRuntimeLLMAnthropicToolNamesArePortableAndReversible(t *testing.T) {
	original := []byte(`{
      "model":"source",
      "input":[{"role":"user","content":[{"type":"input_text","text":"run"}]}],
      "tools":[
        {"type":"web_search_preview"},
        {"type":"function","name":"container.exec","description":"run","parameters":{"type":"object"}}
      ]
    }`)
	translated, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolResponses, runtimeLLMProtocolAnthropic,
		"claude-target", original, false,
	)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	root := decodeRuntimeLLMJSON(t, translated)
	tools := anySlice(root["tools"])
	if len(tools) != 1 {
		t.Fatalf("portable tools = %d, want 1: %s", len(tools), translated)
	}
	tool, _ := tools[0].(map[string]any)
	if got := stringValue(tool["name"]); got != "container_exec" {
		t.Fatalf("normalized tool name = %q: %s", got, translated)
	}

	response, err := convertRuntimeLLMResponse(
		context.Background(), runtimeLLMProtocolAnthropic, runtimeLLMProtocolResponses,
		"claude-target", original, translated,
		[]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-target","content":[{"type":"tool_use","id":"call_1","name":"container_exec","input":{"command":"pwd"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`),
	)
	if err != nil {
		t.Fatalf("convert response: %v", err)
	}
	if !strings.Contains(string(response), `"name":"container.exec"`) {
		t.Fatalf("original tool name was not restored: %s", response)
	}
}

func TestRuntimeLLMGeminiMultimodalDirections(t *testing.T) {
	t.Run("responses PDF to Gemini", func(t *testing.T) {
		converted, err := convertRuntimeLLMRequest(
			runtimeLLMProtocolResponses, runtimeLLMProtocolGemini, "gemini-target",
			[]byte(`{"input":[{"role":"user","content":[{"type":"input_file","filename":"report.pdf","file_data":"data:application/pdf;base64,cGRm"}]}]}`),
			false,
		)
		if err != nil {
			t.Fatalf("convert request: %v", err)
		}
		if !strings.Contains(string(converted), "application/pdf") || !strings.Contains(string(converted), "cGRm") {
			t.Fatalf("Gemini inline PDF was not preserved: %s", converted)
		}
	})

	t.Run("Gemini camelCase PDF to Anthropic", func(t *testing.T) {
		converted, err := convertRuntimeLLMRequest(
			runtimeLLMProtocolGemini, runtimeLLMProtocolAnthropic, "claude-target",
			[]byte(`{"contents":[{"role":"user","parts":[{"text":"inspect"},{"inlineData":{"mimeType":"application/pdf","data":"cGRm"}}]}]}`),
			false,
		)
		if err != nil {
			t.Fatalf("convert request: %v", err)
		}
		root := decodeRuntimeLLMJSON(t, converted)
		if countAnthropicContentType(root, "document") != 1 {
			t.Fatalf("Gemini PDF was not preserved as an Anthropic document: %s", converted)
		}
	})

	t.Run("Gemini image to Responses", func(t *testing.T) {
		converted, err := convertRuntimeLLMRequest(
			runtimeLLMProtocolGemini, runtimeLLMProtocolResponses, "gpt-target",
			[]byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}]}`),
			false,
		)
		if err != nil {
			t.Fatalf("convert request: %v", err)
		}
		if !strings.Contains(string(converted), "input_image") || !strings.Contains(string(converted), "image/png") {
			t.Fatalf("Gemini image was not preserved: %s", converted)
		}
	})
}

func decodeRuntimeLLMJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("decode JSON: %v: %s", err, body)
	}
	return root
}

func countAnthropicContentType(root map[string]any, want string) int {
	count := 0
	walkAnthropicContent(root, func(part map[string]any) {
		if stringValue(part["type"]) == want {
			count++
		}
	})
	return count
}

func countResponsesContentType(root map[string]any, want string) int {
	count := 0
	for _, itemValue := range anySlice(root["input"]) {
		item, _ := itemValue.(map[string]any)
		for _, partValue := range anySlice(item["content"]) {
			part, _ := partValue.(map[string]any)
			if stringValue(part["type"]) == want {
				count++
			}
		}
	}
	return count
}

func findAnthropicToolResult(root map[string]any, id string) map[string]any {
	var found map[string]any
	walkAnthropicContent(root, func(part map[string]any) {
		if stringValue(part["type"]) == "tool_result" && stringValue(part["tool_use_id"]) == id {
			found = part
		}
	})
	return found
}

func findResponsesToolOutput(root map[string]any, id string) any {
	for _, itemValue := range anySlice(root["input"]) {
		item, _ := itemValue.(map[string]any)
		if stringValue(item["type"]) == "function_call_output" && stringValue(item["call_id"]) == id {
			return item["output"]
		}
	}
	return nil
}

func contentTypes(parts []any) []string {
	result := make([]string, 0, len(parts))
	for _, partValue := range parts {
		part, _ := partValue.(map[string]any)
		result = append(result, stringValue(part["type"]))
	}
	return result
}

func containsAll(values []string, wants ...string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	for _, want := range wants {
		if !seen[want] {
			return false
		}
	}
	return true
}
