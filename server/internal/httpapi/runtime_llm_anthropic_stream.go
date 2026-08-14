package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"agentbox/internal/platform"

	"github.com/skys-mission/lmm-adapter-go/adapter"
	"github.com/skys-mission/lmm-adapter-go/stream"
)

type runtimeAnthropicStream struct {
	writer          *stream.SSEWriter
	started         bool
	stopped         bool
	textOpen        bool
	textIndex       int
	nextBlockIndex  int
	toolUsed        bool
	tools           map[int]*runtimeAnthropicTool
	toolOrder       []int
	outputTokens    int64
	pendingStop     string
	defaultModel    string
	defaultResponse string
}

type runtimeAnthropicTool struct {
	id        string
	name      string
	arguments strings.Builder
	emitted   bool
}

func newRuntimeAnthropicStream(writer *stream.SSEWriter, model string) *runtimeAnthropicStream {
	return &runtimeAnthropicStream{
		writer:          writer,
		tools:           make(map[int]*runtimeAnthropicTool),
		defaultModel:    model,
		defaultResponse: "msg_agentbox",
	}
}

func (server *Server) convertRuntimeLLMToAnthropicStream(
	w http.ResponseWriter,
	ctx context.Context,
	reader io.Reader,
	target platform.RuntimeLLMTarget,
	from adapter.Protocol,
) {
	converter := newRuntimeAnthropicStream(stream.NewSSEWriter(w), target.ModelID)
	sseReader := stream.NewSSEReader(reader)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		event, err := sseReader.Read()
		if errors.Is(err, io.EOF) {
			if finishErr := converter.finish(""); finishErr != nil {
				server.logger.Warn("runtime LLM Anthropic stream finalization failed", "error", finishErr)
			}
			return
		}
		if err != nil {
			server.writeRuntimeLLMStreamError(w, runtimeLLMProtocolAnthropic, "读取模型流失败")
			return
		}
		if event.Data == "" {
			continue
		}
		if event.Data == "[DONE]" {
			if finishErr := converter.finish(""); finishErr != nil {
				server.logger.Warn("runtime LLM Anthropic stream finalization failed", "error", finishErr)
			}
			return
		}
		if err := converter.convert(from, []byte(event.Data)); err != nil {
			server.logger.Warn("runtime LLM Anthropic stream conversion failed",
				"sandbox", target.SandboxID, "credential", target.CredentialID,
				"from", from, "error", err)
			server.writeRuntimeLLMStreamError(w, runtimeLLMProtocolAnthropic, "模型流格式转换失败")
			return
		}
	}
}

func (s *runtimeAnthropicStream) convert(protocol adapter.Protocol, data []byte) error {
	switch protocol {
	case adapter.ProtocolOpenAIResponses:
		return s.convertResponses(data)
	case adapter.ProtocolOpenAIChat:
		return s.convertChat(data)
	default:
		return fmt.Errorf("unsupported Anthropic stream source: %s", protocol)
	}
}

func (s *runtimeAnthropicStream) convertResponses(data []byte) error {
	var event struct {
		Type        string `json:"type"`
		OutputIndex int    `json:"output_index"`
		Delta       string `json:"delta"`
		Arguments   string `json:"arguments"`
		Item        *struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
		Response *struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Responses stream event: %w", err)
	}

	switch event.Type {
	case "response.created":
		id, model := s.defaultResponse, s.defaultModel
		if event.Response != nil {
			if event.Response.ID != "" {
				id = event.Response.ID
			}
			if event.Response.Model != "" {
				model = event.Response.Model
			}
		}
		return s.start(id, model)
	case "response.content_part.added":
		return s.startText()
	case "response.output_text.delta", "response.refusal.delta":
		if err := s.startText(); err != nil {
			return err
		}
		return s.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": s.textIndex,
			"delta": map[string]any{"type": "text_delta", "text": event.Delta},
		})
	case "response.content_part.done", "response.output_text.done", "response.refusal.done":
		return s.closeText()
	case "response.output_item.added":
		if event.Item != nil && event.Item.Type == "function_call" {
			s.registerTool(event.OutputIndex, event.Item.CallID, event.Item.Name)
		}
		return nil
	case "response.function_call_arguments.delta":
		s.tool(event.OutputIndex).arguments.WriteString(event.Delta)
		return nil
	case "response.function_call_arguments.done":
		tool := s.tool(event.OutputIndex)
		if event.Arguments != "" {
			tool.arguments.Reset()
			tool.arguments.WriteString(event.Arguments)
		}
		return nil
	case "response.output_item.done":
		if event.Item != nil && event.Item.Type == "function_call" {
			tool := s.registerTool(event.OutputIndex, event.Item.CallID, event.Item.Name)
			if tool.arguments.Len() == 0 && event.Item.Arguments != "" {
				tool.arguments.WriteString(event.Item.Arguments)
			}
			return s.emitTool(event.OutputIndex)
		}
		return nil
	case "response.completed":
		if event.Response != nil && event.Response.Usage != nil {
			s.outputTokens = event.Response.Usage.OutputTokens
		}
		return s.finish("")
	case "response.failed":
		if event.Response != nil && event.Response.Error != nil {
			return s.emitError(event.Response.Error.Type, event.Response.Error.Message)
		}
		return s.emitError("api_error", "模型服务返回失败")
	case "error":
		if event.Error != nil {
			return s.emitError(event.Error.Type, event.Error.Message)
		}
		return s.emitError("api_error", "模型服务返回错误")
	default:
		return nil
	}
}

func (s *runtimeAnthropicStream) convertChat(data []byte) error {
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("decode Chat Completions stream event: %w", err)
	}
	if chunk.Error != nil {
		return s.emitError(chunk.Error.Type, chunk.Error.Message)
	}
	if err := s.start(firstNonEmpty(chunk.ID, s.defaultResponse), firstNonEmpty(chunk.Model, s.defaultModel)); err != nil {
		return err
	}
	if chunk.Usage != nil {
		s.outputTokens = chunk.Usage.CompletionTokens
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			if err := s.startText(); err != nil {
				return err
			}
			if err := s.emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": s.textIndex,
				"delta": map[string]any{"type": "text_delta", "text": *choice.Delta.Content},
			}); err != nil {
				return err
			}
		}
		for _, call := range choice.Delta.ToolCalls {
			tool := s.registerTool(call.Index, call.ID, call.Function.Name)
			tool.arguments.WriteString(call.Function.Arguments)
		}
		if choice.FinishReason != "" {
			s.pendingStop = choice.FinishReason
		}
	}
	return nil
}

func (s *runtimeAnthropicStream) start(id, model string) error {
	if s.started || s.stopped {
		return nil
	}
	s.started = true
	return s.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (s *runtimeAnthropicStream) startText() error {
	if s.textOpen {
		return nil
	}
	if err := s.start(s.defaultResponse, s.defaultModel); err != nil {
		return err
	}
	s.textIndex = s.nextBlockIndex
	s.nextBlockIndex++
	s.textOpen = true
	return s.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": s.textIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
}

func (s *runtimeAnthropicStream) closeText() error {
	if !s.textOpen {
		return nil
	}
	s.textOpen = false
	return s.emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": s.textIndex,
	})
}

func (s *runtimeAnthropicStream) registerTool(index int, id, name string) *runtimeAnthropicTool {
	tool, exists := s.tools[index]
	if !exists {
		tool = &runtimeAnthropicTool{}
		s.tools[index] = tool
		s.toolOrder = append(s.toolOrder, index)
	}
	if id != "" {
		tool.id = id
	}
	if name != "" {
		tool.name = name
	}
	return tool
}

func (s *runtimeAnthropicStream) tool(index int) *runtimeAnthropicTool {
	return s.registerTool(index, "", "")
}

func (s *runtimeAnthropicStream) emitTool(index int) error {
	tool := s.tool(index)
	if tool.emitted {
		return nil
	}
	if err := s.closeText(); err != nil {
		return err
	}
	if err := s.start(s.defaultResponse, s.defaultModel); err != nil {
		return err
	}
	tool.emitted = true
	s.toolUsed = true
	blockIndex := s.nextBlockIndex
	s.nextBlockIndex++
	id := firstNonEmpty(tool.id, "call_"+strconv.Itoa(index))
	name := firstNonEmpty(tool.name, "tool")
	if err := s.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": blockIndex,
		"content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}},
	}); err != nil {
		return err
	}
	arguments := tool.arguments.String()
	if arguments == "" {
		arguments = "{}"
	}
	if err := s.emit("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": blockIndex,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": arguments},
	}); err != nil {
		return err
	}
	return s.emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": blockIndex,
	})
}

func (s *runtimeAnthropicStream) finish(reason string) error {
	if s.stopped {
		return nil
	}
	if err := s.start(s.defaultResponse, s.defaultModel); err != nil {
		return err
	}
	if err := s.closeText(); err != nil {
		return err
	}
	for _, index := range s.toolOrder {
		if err := s.emitTool(index); err != nil {
			return err
		}
	}
	if reason == "" {
		reason = s.pendingStop
	}
	if s.toolUsed {
		reason = "tool_use"
	} else {
		switch reason {
		case "length":
			reason = "max_tokens"
		case "content_filter":
			reason = "refusal"
		default:
			reason = "end_turn"
		}
	}
	if err := s.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": s.outputTokens},
	}); err != nil {
		return err
	}
	s.stopped = true
	return s.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (s *runtimeAnthropicStream) emitError(errorType, message string) error {
	if s.stopped {
		return nil
	}
	s.stopped = true
	return s.emit("error", map[string]any{
		"type":  "error",
		"error": map[string]any{"type": firstNonEmpty(errorType, "api_error"), "message": message},
	})
}

func (s *runtimeAnthropicStream) emit(event string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := s.writer.Write(&stream.SSEEvent{Event: event, Data: string(data)}); err != nil {
		return err
	}
	return s.writer.Flush()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
