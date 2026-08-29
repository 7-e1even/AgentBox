package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/platform"
)

type sandboxAgentToolActionInput struct {
	Tools []string `json:"tools"`
}

func (s *Server) operateSandbox(w http.ResponseWriter, request *http.Request) {
	id, action := request.PathValue("id"), request.PathValue("action")
	started := time.Now()
	resource, err := s.store.OperateSandbox(request.Context(), id, action)
	entry := platform.LogEntry{
		Category: platform.LogCategorySandbox, Action: action,
		ResourceKind: string(platform.KindSandbox), ResourceID: id,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = fmt.Sprintf("沙箱 %s 操作 %s 失败", id, action)
		entry.Detail = map[string]any{"error": err.Error()}
		s.recordLog(request, entry)
		s.handleError(w, err)
		return
	}
	entry.ResourceName = resource.Name
	entry.Message = fmt.Sprintf("沙箱 %s 执行操作 %s", resource.Name, action)
	s.recordLog(request, entry)
	s.writeJSON(w, http.StatusAccepted, map[string]any{"resource": resource})
}

func (s *Server) operateSandboxAgentTools(w http.ResponseWriter, request *http.Request) {
	var input sandboxAgentToolActionInput
	if !s.decodeJSONWithLimit(w, request, &input, 4<<10) {
		return
	}
	for index := range input.Tools {
		input.Tools[index] = strings.TrimSpace(input.Tools[index])
	}
	id, action := request.PathValue("id"), request.PathValue("action")
	started := time.Now()
	resource, err := s.store.OperateSandboxAgentTools(request.Context(), id, action, input.Tools)
	entry := platform.LogEntry{
		Category: platform.LogCategorySandbox, Action: "agent-tools-" + action,
		ResourceKind: string(platform.KindSandbox), ResourceID: id,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = fmt.Sprintf("沙箱 %s Agent 工具操作 %s 失败", id, action)
		entry.Detail = map[string]any{"error": err.Error(), "tools": input.Tools}
		s.recordLog(request, entry)
		s.handleError(w, err)
		return
	}
	entry.ResourceName = resource.Name
	entry.Message = fmt.Sprintf("沙箱 %s 执行 Agent 工具操作 %s", resource.Name, action)
	entry.Detail = map[string]any{"tools": input.Tools}
	s.recordLog(request, entry)
	s.writeJSON(w, http.StatusAccepted, map[string]any{"resource": resource})
}
