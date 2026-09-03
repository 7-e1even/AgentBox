package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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

func (s *Server) getAutomationSecret(w http.ResponseWriter, request *http.Request) {
	automation, secret, err := s.store.GetAutomationSecret(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "read-secret",
		Message:      "查看自动化密钥 " + automation.Name,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
	})
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]any{
		"automation":  automation,
		"secret":      secret,
		"webhookPath": "/api/webhooks/" + automation.EndpointID,
	})
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
	s.writeJSON(w, http.StatusOK, map[string]any{
		"automation":  automation,
		"secret":      secret,
		"webhookPath": "/api/webhooks/" + automation.EndpointID,
	})
}

func (s *Server) testAutomation(w http.ResponseWriter, request *http.Request) {
	var input struct{}
	if !s.decodeJSON(w, request, &input) {
		return
	}
	result, err := s.store.TestAutomation(request.Context(), request.PathValue("id"), []byte("{}"))
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
	s.writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) listAutomationRuns(w http.ResponseWriter, request *http.Request) {
	projectID := strings.TrimSpace(request.URL.Query().Get("projectId"))
	if projectID == "" {
		s.writeError(w, http.StatusBadRequest, "请选择所属项目")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	filter := platform.AutomationRunFilter{
		ProjectID:    projectID,
		AutomationID: strings.TrimSpace(request.URL.Query().Get("automationId")),
		Status:       platform.AutomationRunStatus(strings.TrimSpace(request.URL.Query().Get("status"))),
		Search:       strings.TrimSpace(request.URL.Query().Get("search")),
		Cursor:       strings.TrimSpace(request.URL.Query().Get("cursor")),
		Limit:        limit,
	}
	page, err := s.store.ListAutomationRunsPage(request.Context(), filter)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"items": page.Items, "runs": page.Items,
		"nextCursor": page.NextCursor, "hasMore": page.HasMore,
	})
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
	clientIP := s.clientIP(request)
	if s.webhookLimiter != nil && !s.webhookLimiter.allowPreAuthentication(clientIP, time.Now()) {
		w.Header().Set("Retry-After", "60")
		s.writeAutomationWebhookError(w, http.StatusTooManyRequests, "rate_limited", "Webhook 请求过于频繁", true)
		return
	}
	// endpoint_id 是 UUID 列：非法格式会让 Postgres 转型报错，
	// 提前拦截，避免把 "地址不存在" 误报成 500。
	if _, err := uuid.Parse(request.PathValue("endpointId")); err != nil {
		s.writeAutomationWebhookError(w, http.StatusNotFound, "not_found", "Webhook 不存在", false)
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			s.writeAutomationWebhookError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Webhook 请求不能超过 1 MiB", false)
			return
		}
		s.writeAutomationWebhookError(w, http.StatusBadRequest, "invalid_request", "无法读取 Webhook 请求", false)
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
	reservedAt := time.Now()
	if s.webhookLimiter != nil && !s.webhookLimiter.reserveBusinessCapacity(request.PathValue("endpointId"), reservedAt) {
		w.Header().Set("Retry-After", "60")
		s.writeAutomationWebhookError(w, http.StatusTooManyRequests, "rate_limited", "Webhook 请求过于频繁", true)
		return
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
		if s.webhookLimiter != nil && (errors.Is(err, store.ErrResourceNotFound) ||
			errors.Is(err, store.ErrWebhookUnauthorized) || platform.IsAutomationValidationError(err)) {
			s.webhookLimiter.releaseBusinessCapacity(request.PathValue("endpointId"), reservedAt)
		}
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAutomation, Action: "webhook",
			Message: "Webhook 触发失败：" + request.PathValue("endpointId"), Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleAutomationTriggerError(w, err)
		return
	}
	status := http.StatusAccepted
	if result.Duplicate {
		status = http.StatusOK
	}
	statusURL := s.workerRequestBaseURL(request) + "/api/webhooks/" +
		request.PathValue("endpointId") + "/runs/" + result.Run.ID
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "2")
	response := map[string]any{
		"runId": result.Run.ID, "sandboxId": result.Run.SandboxID,
		"status": result.Run.Status, "duplicate": result.Duplicate,
		"statusUrl": statusURL, "runToken": result.StatusToken, "pollAfterSeconds": 2,
	}
	if result.Run.ErrorMessage != "" {
		response["error"] = result.Run.ErrorMessage
	}
	s.writeJSON(w, status, response)
}

func (s *Server) getPublicAutomationRun(w http.ResponseWriter, request *http.Request) {
	if _, err := uuid.Parse(request.PathValue("endpointId")); err != nil {
		s.writeAutomationWebhookError(w, http.StatusNotFound, "not_found", "Run 不存在", false)
		return
	}
	if _, err := uuid.Parse(request.PathValue("runId")); err != nil {
		s.writeAutomationWebhookError(w, http.StatusNotFound, "not_found", "Run 不存在", false)
		return
	}
	prefix, token, found := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(prefix, "Bearer") || token == "" {
		s.writeAutomationWebhookError(w, http.StatusUnauthorized, "run_token_invalid", "Run Token 无效", false)
		return
	}
	run, err := s.store.GetPublicAutomationRun(request.Context(), request.PathValue("endpointId"), request.PathValue("runId"), token)
	if err != nil {
		if errors.Is(err, store.ErrWebhookUnauthorized) {
			s.writeAutomationWebhookError(w, http.StatusUnauthorized, "run_token_invalid", "Run Token 无效", false)
			return
		}
		if errors.Is(err, store.ErrResourceNotFound) {
			s.writeAutomationWebhookError(w, http.StatusNotFound, "not_found", "Run 不存在", false)
			return
		}
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleAutomationTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrWebhookUnauthorized):
		s.writeAutomationWebhookError(w, http.StatusUnauthorized, "authentication_failed", "Webhook 认证无效", false)
	case errors.Is(err, store.ErrAutomationDisabled):
		s.writeAutomationWebhookError(w, http.StatusGone, "automation_disabled", "自动化已停用", false)
	case errors.Is(err, store.ErrAutomationRateLimit):
		w.Header().Set("Retry-After", "60")
		s.writeAutomationWebhookError(w, http.StatusTooManyRequests, "rate_limited", "自动化触发过于频繁，请稍后重试", true)
	case errors.Is(err, store.ErrAutomationIdempotencyConflict):
		s.writeAutomationWebhookError(w, http.StatusConflict, "idempotency_conflict", "相同 Idempotency-Key 已用于不同 Payload", false)
	case platform.IsAutomationValidationError(err), platform.IsValidationError(err):
		s.writeAutomationWebhookError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, store.ErrResourceNotFound):
		s.writeAutomationWebhookError(w, http.StatusNotFound, "not_found", "Webhook 不存在", false)
	default:
		s.logger.Error("automation webhook failed", "error", err)
		s.writeAutomationWebhookError(w, http.StatusInternalServerError, "internal_error", "Webhook 处理失败", true)
	}
}

func (s *Server) writeAutomationWebhookError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	s.writeAPIError(w, status, code, message, retryable)
}
