package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/platform"

	"github.com/skys-mission/lmm-adapter-go/adapter"
	"github.com/skys-mission/lmm-adapter-go/stream"
)

type runtimeResponsesStream struct {
	writer        *stream.SSEWriter
	started       bool
	stopped       bool
	responseID    string
	model         string
	createdAt     int64
	nextOutput    int
	active        *runtimeResponsesItem
	output        []map[string]any
	inputTokens   int64
	outputTokens  int64
	pendingStop   string
	defaultModel  string
	defaultRespID string
	toolNames     map[string]string
}

type runtimeResponsesItem struct {
	kind        string
	outputIndex int
	itemID      string
	callID      string
	name        string
	text        strings.Builder
	arguments   strings.Builder
}

func newRuntimeResponsesStream(
	writer *stream.SSEWriter, model string, toolNames map[string]string,
) *runtimeResponsesStream {
	return &runtimeResponsesStream{
		writer:        writer,
		createdAt:     time.Now().Unix(),
		defaultModel:  model,
		defaultRespID: "resp_agentbox",
		toolNames:     toolNames,
	}
}

func (server *Server) convertRuntimeLLMToResponsesStream(
	w http.ResponseWriter,
	ctx context.Context,
	reader io.Reader,
	target platform.RuntimeLLMTarget,
	toolNames map[string]string,
) {
	converter := newRuntimeResponsesStream(stream.NewSSEWriter(w), target.ModelID, toolNames)
	sseReader := stream.NewSSEReader(reader)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		event, err := sseReader.Read()
		if errors.Is(err, io.EOF) {
			if finishErr := converter.finish(); finishErr != nil {
				server.logger.Warn("runtime LLM Responses stream finalization failed", "error", finishErr)
			}
			return
		}
		if err != nil {
			server.writeRuntimeLLMStreamError(w, runtimeLLMProtocolResponses, "读取模型流失败")
			return
		}
		if event.Data == "" {
			continue
		}
		if event.Data == "[DONE]" {
			if finishErr := converter.finish(); finishErr != nil {
				server.logger.Warn("runtime LLM Responses stream finalization failed", "error", finishErr)
			}
			return
		}
		if err := converter.convertAnthropic([]byte(event.Data)); err != nil {
			server.logger.Warn("runtime LLM Responses stream conversion failed",
				"sandbox", target.SandboxID, "credential", target.CredentialID,
				"from", adapter.ProtocolClaudeMessages, "error", err)
			server.writeRuntimeLLMStreamError(w, runtimeLLMProtocolResponses, "模型流格式转换失败")
			return
		}
	}
}

func (s *runtimeResponsesStream) convertAnthropic(data []byte) error {
	var event struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message *struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		ContentBlock *struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Text  string          `json:"text"`
			Input json.RawMessage `json:"input"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Anthropic stream event: %w", err)
	}

	switch event.Type {
	case "message_start":
		id, model := s.defaultRespID, s.defaultModel
		if event.Message != nil {
			id = responsesID(event.Message.ID)
			model = firstNonEmpty(event.Message.Model, model)
			if event.Message.Usage != nil {
				s.inputTokens = event.Message.Usage.InputTokens
				s.outputTokens = event.Message.Usage.OutputTokens
			}
		}
		return s.start(id, model)
	case "content_block_start":
		if event.ContentBlock == nil {
			return nil
		}
		if err := s.closeActive(); err != nil {
			return err
		}
		switch event.ContentBlock.Type {
		case "text":
			if err := s.startText(); err != nil {
				return err
			}
			if event.ContentBlock.Text != "" {
				return s.textDelta(event.ContentBlock.Text)
			}
		case "tool_use":
			if err := s.startTool(event.ContentBlock.ID, event.ContentBlock.Name); err != nil {
				return err
			}
			if initial := strings.TrimSpace(string(event.ContentBlock.Input)); initial != "" && initial != "{}" && initial != "null" {
				return s.toolDelta(initial)
			}
		}
		return nil
	case "content_block_delta":
		if event.Delta == nil {
			return nil
		}
		switch event.Delta.Type {
		case "text_delta":
			return s.textDelta(event.Delta.Text)
		case "input_json_delta":
			return s.toolDelta(event.Delta.PartialJSON)
		}
		return nil
	case "content_block_stop":
		return s.closeActive()
	case "message_delta":
		if event.Delta != nil {
			s.pendingStop = event.Delta.StopReason
		}
		if event.Usage != nil {
			if event.Usage.InputTokens > 0 {
				s.inputTokens = event.Usage.InputTokens
			}
			s.outputTokens = event.Usage.OutputTokens
		}
		return nil
	case "message_stop":
		return s.finish()
	case "error":
		if event.Error == nil {
			return s.emit("error", map[string]any{
				"type": "error", "error": map[string]any{"type": "api_error", "message": "模型服务返回错误"},
			})
		}
		return s.emit("error", map[string]any{
			"type": "error", "error": map[string]any{"type": event.Error.Type, "message": event.Error.Message},
		})
	default:
		return nil
	}
}

func (s *runtimeResponsesStream) start(id, model string) error {
	if s.started || s.stopped {
		return nil
	}
	s.started = true
	s.responseID = firstNonEmpty(id, s.defaultRespID)
	s.model = firstNonEmpty(model, s.defaultModel)
	return s.emit("response.created", map[string]any{
		"type":     "response.created",
		"response": s.response("in_progress", []map[string]any{}),
	})
}

func (s *runtimeResponsesStream) startText() error {
	if err := s.start(s.defaultRespID, s.defaultModel); err != nil {
		return err
	}
	index := s.nextOutput
	s.nextOutput++
	itemID := fmt.Sprintf("msg_agentbox_%d", index)
	s.active = &runtimeResponsesItem{kind: "text", outputIndex: index, itemID: itemID}
	if err := s.emit("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": index,
		"item": map[string]any{
			"id": itemID, "type": "message", "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	}); err != nil {
		return err
	}
	return s.emit("response.content_part.added", map[string]any{
		"type": "response.content_part.added", "item_id": itemID,
		"output_index": index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (s *runtimeResponsesStream) textDelta(text string) error {
	if s.active == nil || s.active.kind != "text" {
		if err := s.closeActive(); err != nil {
			return err
		}
		if err := s.startText(); err != nil {
			return err
		}
	}
	s.active.text.WriteString(text)
	return s.emit("response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": s.active.itemID,
		"output_index": s.active.outputIndex, "content_index": 0,
		"delta": text, "logprobs": []any{},
	})
}

func (s *runtimeResponsesStream) startTool(callID, name string) error {
	if err := s.start(s.defaultRespID, s.defaultModel); err != nil {
		return err
	}
	index := s.nextOutput
	s.nextOutput++
	callID = firstNonEmpty(callID, fmt.Sprintf("call_agentbox_%d", index))
	itemID := fmt.Sprintf("fc_agentbox_%d", index)
	if original := s.toolNames[name]; original != "" {
		name = original
	}
	s.active = &runtimeResponsesItem{
		kind: "tool", outputIndex: index, itemID: itemID,
		callID: callID, name: firstNonEmpty(name, "tool"),
	}
	return s.emit("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": index,
		"item": map[string]any{
			"id": itemID, "type": "function_call", "status": "in_progress",
			"arguments": "", "call_id": callID, "name": s.active.name,
		},
	})
}

func (s *runtimeResponsesStream) toolDelta(arguments string) error {
	if s.active == nil || s.active.kind != "tool" {
		return nil
	}
	s.active.arguments.WriteString(arguments)
	return s.emit("response.function_call_arguments.delta", map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": s.active.itemID,
		"output_index": s.active.outputIndex, "delta": arguments,
	})
}

func (s *runtimeResponsesStream) closeActive() error {
	if s.active == nil {
		return nil
	}
	item := s.active
	s.active = nil
	switch item.kind {
	case "text":
		text := item.text.String()
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		completed := map[string]any{
			"id": item.itemID, "type": "message", "status": "completed",
			"role": "assistant", "content": []any{part},
		}
		for _, event := range []struct {
			name    string
			payload map[string]any
		}{
			{"response.output_text.done", map[string]any{
				"type": "response.output_text.done", "item_id": item.itemID,
				"output_index": item.outputIndex, "content_index": 0,
				"text": text, "logprobs": []any{},
			}},
			{"response.content_part.done", map[string]any{
				"type": "response.content_part.done", "item_id": item.itemID,
				"output_index": item.outputIndex, "content_index": 0, "part": part,
			}},
			{"response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": item.outputIndex, "item": completed,
			}},
		} {
			if err := s.emit(event.name, event.payload); err != nil {
				return err
			}
		}
		s.output = append(s.output, completed)
	case "tool":
		arguments := item.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		completed := map[string]any{
			"id": item.itemID, "type": "function_call", "status": "completed",
			"arguments": arguments, "call_id": item.callID, "name": item.name,
		}
		if err := s.emit("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": item.itemID,
			"output_index": item.outputIndex, "arguments": arguments,
		}); err != nil {
			return err
		}
		if err := s.emit("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": item.outputIndex, "item": completed,
		}); err != nil {
			return err
		}
		s.output = append(s.output, completed)
	}
	return nil
}

func (s *runtimeResponsesStream) finish() error {
	if s.stopped {
		return nil
	}
	if err := s.start(s.defaultRespID, s.defaultModel); err != nil {
		return err
	}
	if err := s.closeActive(); err != nil {
		return err
	}
	s.stopped = true
	return s.emit("response.completed", map[string]any{
		"type": "response.completed", "response": s.response("completed", s.output),
	})
}

func (s *runtimeResponsesStream) response(status string, output []map[string]any) map[string]any {
	return map[string]any{
		"id": s.responseID, "object": "response", "created_at": s.createdAt,
		"model": s.model, "status": status, "output": output,
		"error": nil, "incomplete_details": nil,
		"usage": map[string]any{
			"input_tokens": s.inputTokens, "output_tokens": s.outputTokens,
			"total_tokens": s.inputTokens + s.outputTokens,
		},
	}
}

func (s *runtimeResponsesStream) emit(event string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := s.writer.Write(&stream.SSEEvent{Data: string(data)}); err != nil {
		return err
	}
	return s.writer.Flush()
}

func responsesID(messageID string) string {
	if suffix, ok := strings.CutPrefix(messageID, "msg_"); ok {
		return "resp_" + suffix
	}
	if strings.HasPrefix(messageID, "resp_") {
		return messageID
	}
	return "resp_agentbox"
}
