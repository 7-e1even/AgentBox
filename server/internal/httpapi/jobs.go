package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

func (s *Server) claimWorkerJob(w http.ResponseWriter, request *http.Request) {
	credential := authBearer(request)
	job, err := s.store.ClaimWorkerJob(
		request.Context(), request.PathValue("id"), credential,
	)
	if errors.Is(err, store.ErrNoJob) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	baseURL := workerRequestBaseURL(request, s.trustedProxy)
	attachWorkerRuntimeEndpoints(job.Payload, baseURL)
	attachWorkerProxyEndpoint(job.Payload, baseURL)
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryJob, Action: "claim",
		Message:      "Worker 认领任务：" + job.Action,
		ResourceKind: "server", ResourceID: request.PathValue("id"),
		Detail: map[string]any{"jobId": job.ID, "jobAction": job.Action, "resourceId": job.ResourceID},
	})
	s.writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func workerRequestBaseURL(request *http.Request, trustedProxy bool) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	host := request.Host
	if trustedProxy {
		// 仅在信任代理时才采纳 X-Forwarded-Proto/Host，防止伪造头污染 Worker 回连地址。
		if forwarded := firstForwardedValue(request.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
		if forwarded := firstForwardedValue(request.Header.Get("X-Forwarded-Host")); forwarded != "" {
			host = forwarded
		}
	}
	parsed, err := url.Parse(scheme + "://" + host)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return strings.TrimRight(parsed.String(), "/")
}

func firstForwardedValue(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

func attachWorkerRuntimeEndpoints(payload map[string]any, baseURL string) {
	if baseURL == "" {
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return
	}
	payload["controlPlane"] = map[string]any{"allowNet": []string{parsed.Hostname()}}
	credentials, ok := payload["credentials"].([]map[string]any)
	if !ok {
		return
	}
	for _, credential := range credentials {
		facadePath, _ := credential["facadePath"].(string)
		protocol, _ := credential["protocol"].(string)
		if facadePath == "" {
			continue
		}
		root := baseURL + "/" + strings.TrimLeft(facadePath, "/")
		credential["anthropicEndpoint"] = root + "/anthropic"
		credential["openaiEndpoint"] = root + "/openai/v1"
		credential["chatEndpoint"] = root + "/openai/v1"
		switch protocol {
		case "anthropic":
			credential["endpoint"] = credential["anthropicEndpoint"]
		case "gemini":
			credential["endpoint"] = root + "/gemini"
		default:
			credential["endpoint"] = credential["openaiEndpoint"]
		}
	}
}

func attachWorkerProxyEndpoint(payload map[string]any, baseURL string) {
	proxy, ok := payload["proxy"].(map[string]any)
	if !ok || baseURL == "" {
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return
	}
	proxyHost, _ := proxy["host"].(string)
	noProxy := uniqueStrings([]string{"localhost", "127.0.0.1", "::1"}, proxy["noProxy"], parsed.Hostname())
	allowNet := uniqueStrings([]string{proxyHost}, proxy["noProxy"], parsed.Hostname())
	proxy["noProxy"] = noProxy
	proxy["allowNet"] = removeLocalProxyHosts(allowNet)
}

func uniqueStrings(initial []string, extra any, final string) []string {
	result := make([]string, 0, len(initial)+4)
	seen := make(map[string]bool)
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			return
		}
		seen[key] = true
		result = append(result, value)
	}
	for _, value := range initial {
		appendValue(value)
	}
	switch values := extra.(type) {
	case []string:
		for _, value := range values {
			appendValue(value)
		}
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				appendValue(text)
			}
		}
	}
	appendValue(final)
	return result
}

func removeLocalProxyHosts(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "localhost", "127.0.0.1", "::1":
			continue
		}
		result = append(result, value)
	}
	return result
}

func (s *Server) reportWorkerJobProgress(w http.ResponseWriter, request *http.Request) {
	var input platform.WorkerJobProgressInput
	if !s.decodeJSONWithLimit(w, request, &input, 4<<10) {
		return
	}
	input.Stage = strings.TrimSpace(input.Stage)
	input.Message = strings.TrimSpace(input.Message)
	input.CacheStatus = strings.TrimSpace(input.CacheStatus)
	input.CacheReason = strings.TrimSpace(input.CacheReason)
	input.AgentTool = strings.TrimSpace(input.AgentTool)
	input.AgentToolStatus = strings.TrimSpace(input.AgentToolStatus)
	validAgentToolStatus := false
	switch input.AgentToolStatus {
	case "", "running", "installed", "verifying", "succeeded", "failed", "cached":
		validAgentToolStatus = true
	}
	if input.Stage == "" || len(input.Stage) > 64 || len(input.Message) > 500 ||
		len(input.CacheStatus) > 32 || len(input.CacheReason) > 200 ||
		len(input.AgentTool) > 64 || !validAgentToolStatus ||
		(input.AgentTool == "") != (input.AgentToolStatus == "") {
		s.writeError(w, http.StatusBadRequest, "Worker 进度无效")
		return
	}
	progress, err := s.store.ReportWorkerJobProgress(
		request.Context(), request.PathValue("id"), authBearer(request),
		request.PathValue("jobId"), input,
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"progress": progress})
}

func (s *Server) completeWorkerJob(w http.ResponseWriter, request *http.Request) {
	var result platform.WorkerJobResult
	if !s.decodeJSONWithLimit(w, request, &result, 8<<20) {
		return
	}
	if !normalizeWorkerJobResult(&result) {
		s.writeError(w, http.StatusBadRequest, "Worker 错误详情无效")
		return
	}
	if len(result.Message) > 768<<10 || len(result.Output) > 768<<10 || len(result.ExternalID) > 255 {
		s.writeError(w, http.StatusBadRequest, "Worker 结果过长")
		return
	}
	credential := authBearer(request)
	if err := s.store.CompleteWorkerJob(
		request.Context(), request.PathValue("id"), credential,
		request.PathValue("jobId"), result,
	); err != nil {
		s.handleError(w, err)
		return
	}
	message := result.Message
	if len(message) > 500 {
		message = message[:500]
	}
	entry := platform.LogEntry{
		Category: platform.LogCategoryJob, Action: "complete",
		Message:      "Worker 任务完成",
		ResourceKind: "server", ResourceID: request.PathValue("id"),
		Detail: map[string]any{"jobId": request.PathValue("jobId"), "externalId": result.ExternalID},
	}
	if message != "" {
		entry.Detail["message"] = message
	}
	if !result.Success {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = "Worker 任务失败"
		if result.Error != nil {
			entry.Detail["errorCode"] = result.Error.Code
			entry.Detail["errorStage"] = result.Error.Stage
			entry.Detail["retryable"] = result.Error.Retryable
		}
	}
	s.recordLog(request, entry)
	w.WriteHeader(http.StatusNoContent)
}

func normalizeWorkerJobResult(result *platform.WorkerJobResult) bool {
	if !normalizeWorkerAgentTools(result.AgentTools) {
		return false
	}
	if result.Error == nil {
		return true
	}
	if result.Success {
		return false
	}
	result.Error.Code = strings.TrimSpace(result.Error.Code)
	result.Error.Stage = strings.TrimSpace(result.Error.Stage)
	if !validWorkerErrorToken(result.Error.Code, true) ||
		!validWorkerErrorToken(result.Error.Stage, false) || len(result.Error.Details) > 16 {
		return false
	}
	for key, value := range result.Error.Details {
		if !validWorkerErrorDetailKey(key) || len(value) > 500 {
			return false
		}
	}
	encoded, err := json.Marshal(result.Error.Details)
	return err == nil && len(encoded) <= 8<<10
}

func normalizeWorkerAgentTools(tools []platform.SandboxAgentToolState) bool {
	if len(tools) > 32 {
		return false
	}
	seen := make(map[string]struct{}, len(tools))
	for index := range tools {
		tool := &tools[index]
		tool.Tool = strings.TrimSpace(tool.Tool)
		tool.CurrentVersion = strings.TrimSpace(tool.CurrentVersion)
		tool.LatestVersion = strings.TrimSpace(tool.LatestVersion)
		tool.PreviousVersion = strings.TrimSpace(tool.PreviousVersion)
		tool.Status = strings.TrimSpace(tool.Status)
		tool.Message = strings.TrimSpace(tool.Message)
		tool.Source = strings.TrimSpace(tool.Source)
		if !validSandboxAgentTool(tool.Tool) || !validSandboxAgentToolStatus(tool.Status) ||
			len(tool.CurrentVersion) > 128 || len(tool.LatestVersion) > 128 ||
			len(tool.PreviousVersion) > 128 || len(tool.Message) > 500 || len(tool.Source) > 64 ||
			tool.CheckedAt.IsZero() {
			return false
		}
		if _, exists := seen[tool.Tool]; exists {
			return false
		}
		seen[tool.Tool] = struct{}{}
	}
	return true
}

func validSandboxAgentTool(tool string) bool {
	switch tool {
	case "claude-code", "codex", "deepseek-harness", "gemini-cli", "grok", "kimi", "opencode", "pi", "reasonix":
		return true
	default:
		return false
	}
}

func validSandboxAgentToolStatus(status string) bool {
	switch status {
	case "installed", "not-installed", "broken", "updated", "unchanged", "failed":
		return true
	default:
		return false
	}
}

func validWorkerErrorDetailKey(key string) bool {
	switch key {
	case "action", "driver", "runtime":
		return true
	default:
		return false
	}
}

func validWorkerErrorToken(value string, required bool) bool {
	if value == "" {
		return !required
	}
	if len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
