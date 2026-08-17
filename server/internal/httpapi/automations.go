package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"agentbox/internal/platform"
	"agentbox/internal/store"

	"github.com/google/uuid"
)

func (s *Server) listAutomations(w http.ResponseWriter, request *http.Request) {
	projectID := strings.TrimSpace(request.URL.Query().Get("projectId"))
	if projectID == "" {
		s.writeError(w, http.StatusBadRequest, "请选择所属项目")
		return
	}
	automations, err := s.store.ListAutomations(request.Context(), projectID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"automations": automations})
}

func (s *Server) getAutomation(w http.ResponseWriter, request *http.Request) {
	automation, err := s.store.GetAutomation(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"automation": automation})
}

func (s *Server) createAutomation(w http.ResponseWriter, request *http.Request) {
	var input platform.AutomationInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	automation, secret, err := s.store.CreateAutomation(
		request.Context(), input, userFromContext(request.Context()).ID,
	)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "create",
			Message: "创建自动化 " + input.Name + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "create",
		Message:      "创建自动化 " + automation.Name,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
	})
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"automation":  automation,
		"secret":      secret,
		"webhookPath": "/api/webhooks/" + automation.EndpointID,
	})
}

func (s *Server) updateAutomation(w http.ResponseWriter, request *http.Request) {
	var input platform.AutomationInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	automation, err := s.store.UpdateAutomation(
		request.Context(), request.PathValue("id"), input, userFromContext(request.Context()).ID,
	)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "update",
			Message: "更新自动化 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "automation", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "update",
		Message:      "更新自动化 " + automation.Name,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
		Detail: map[string]any{"enabled": automation.Enabled},
	})
	s.writeJSON(w, http.StatusOK, map[string]any{"automation": automation})
}

func (s *Server) deleteAutomation(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteAutomation(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "delete",
			Message: "删除自动化 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "automation", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "delete",
		Message:      "删除自动化 " + request.PathValue("id"),
		ResourceKind: "automation", ResourceID: request.PathValue("id"),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateAutomationSecret(w http.ResponseWriter, request *http.Request) {
	automation, secret, err := s.store.RotateAutomationSecret(
		request.Context(), request.PathValue("id"), userFromContext(request.Context()).ID,
	)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "rotate-secret",
			Message: "轮换自动化密钥 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "automation", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "rotate-secret",
		Message:      "轮换自动化密钥 " + automation.Name,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
	})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"automation":  automation,
		"secret":      secret,
		"webhookPath": "/api/webhooks/" + automation.EndpointID,
	})
}

func (s *Server) previewAutomation(w http.ResponseWriter, request *http.Request) {
	var input platform.AutomationPreviewInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	preview, err := s.store.PreviewAutomation(request.Context(), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"input": preview})
}

func (s *Server) testAutomation(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Payload any `json:"payload"`
	}
	if !s.decodeJSON(w, request, &input) {
		return
	}
	body, err := json.Marshal(input.Payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "测试 Payload 无效")
		return
	}
	result, err := s.store.TestAutomation(request.Context(), request.PathValue("id"), body)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "test",
			Message: "测试自动化 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "automation", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleAutomationTriggerError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "test",
		Message:      "测试自动化 " + result.Run.AutomationName,
		ResourceKind: "automation", ResourceID: request.PathValue("id"), ResourceName: result.Run.AutomationName,
		Detail: map[string]any{"runId": result.Run.ID, "status": string(result.Run.Status)},
	})
	s.writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) listAutomationRuns(w http.ResponseWriter, request *http.Request) {
	projectID := strings.TrimSpace(request.URL.Query().Get("projectId"))
	if projectID == "" {
		s.writeError(w, http.StatusBadRequest, "请选择所属项目")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	runs, err := s.store.ListAutomationRuns(
		request.Context(), projectID, strings.TrimSpace(request.URL.Query().Get("automationId")), limit,
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) getAutomationRun(w http.ResponseWriter, request *http.Request) {
	run, err := s.store.GetAutomationRun(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) receiveAutomationWebhook(w http.ResponseWriter, request *http.Request) {
	// endpoint_id 是 UUID 列：非法格式会让 Postgres 转型报错，
	// 提前拦截，避免把 "地址不存在" 误报成 500。
	if _, err := uuid.Parse(request.PathValue("endpointId")); err != nil {
		s.writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "Webhook 请求不能超过 1 MiB")
			return
		}
		s.writeError(w, http.StatusBadRequest, "无法读取 Webhook 请求")
		return
	}
	headers := make(map[string]string, len(request.Header))
	for key, values := range request.Header {
		headers[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	query := make(map[string]any, len(request.URL.Query()))
	for key, values := range request.URL.Query() {
		if len(values) == 1 {
			query[key] = values[0]
		} else {
			query[key] = values
		}
	}
	result, err := s.store.TriggerAutomation(request.Context(), platform.AutomationDelivery{
		EndpointID:     request.PathValue("endpointId"),
		Authorization:  request.Header.Get("Authorization"),
		Timestamp:      request.Header.Get("X-AgentBox-Timestamp"),
		Signature:      request.Header.Get("X-AgentBox-Signature"),
		IdempotencyKey: request.Header.Get("Idempotency-Key"),
		Body:           body,
		Headers:        headers,
		Query:          query,
	})
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "webhook",
			Message: "Webhook 触发失败：" + request.PathValue("endpointId"), Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleAutomationTriggerError(w, err)
		return
	}
	entry := platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "webhook",
		Message:      "Webhook 触发自动化 " + result.Run.AutomationName,
		ResourceKind: "automation", ResourceName: result.Run.AutomationName,
		Detail: map[string]any{
			"runId": result.Run.ID, "status": string(result.Run.Status),
			"duplicate": result.Duplicate, "endpointId": request.PathValue("endpointId"),
		},
	}
	if result.Run.AutomationID != nil {
		entry.ResourceID = *result.Run.AutomationID
	}
	if result.Run.ErrorMessage != "" {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Detail["error"] = result.Run.ErrorMessage
	}
	s.recordLog(request, entry)
	status := http.StatusAccepted
	if result.Duplicate {
		status = http.StatusOK
	}
	response := map[string]any{
		"runId": result.Run.ID, "sandboxId": result.Run.SandboxID,
		"status": result.Run.Status, "duplicate": result.Duplicate,
	}
	if result.Run.ErrorMessage != "" {
		response["error"] = result.Run.ErrorMessage
	}
	s.writeJSON(w, status, response)
}

func (s *Server) handleAutomationTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrWebhookUnauthorized):
		s.writeError(w, http.StatusUnauthorized, "Webhook 认证无效")
	case errors.Is(err, store.ErrAutomationDisabled):
		s.writeError(w, http.StatusGone, "自动化已停用")
	case errors.Is(err, store.ErrAutomationRateLimit):
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "自动化触发过于频繁，请稍后重试")
	default:
		s.handleError(w, err)
	}
}
