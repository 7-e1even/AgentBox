package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"agentbox/internal/platform"
)

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
