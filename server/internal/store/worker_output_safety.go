package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/platform"
)

const workerFailClosedJobOutputCapability = "fail-closed-job-output"

func sanitizeWorkerJobCompletion(
	action, resourceID string,
	payloadJSON []byte,
	result *platform.WorkerJobResult,
	now time.Time,
) error {
	if result == nil {
		return nil
	}
	if isSandboxLifecycleAction(action) {
		externalID, err := trustedSandboxCompletionExternalID(action, resourceID, payloadJSON, result.Success)
		if err != nil {
			return err
		}
		// The Worker value is never persisted, even when it happens to match.
		result.ExternalID = externalID
	} else if isGuestInteractingWorkerAction(action) {
		result.ExternalID = ""
	}

	if action == "check-network-proxy" {
		result.Output = canonicalNetworkProxyCheckOutput(result.Output, now)
	} else {
		// Arbitrary Worker output is not an audit-safe persistence format.
		result.Output = ""
	}
	result.OutputTruncated = false

	if isGuestInteractingWorkerAction(action) {
		sanitizeWorkerJobErrorMetadata(action, result)
		result.Message = safeWorkerCompletionMessage(action, *result)
		if isSandboxAgentToolAction(action) {
			result.AgentTools = safeWorkerAgentToolStates(result.AgentTools, now)
		} else {
			result.AgentTools = nil
		}
	}
	return nil
}

func sanitizeWorkerJobErrorMetadata(action string, result *platform.WorkerJobResult) {
	if result.Success {
		result.TimedOut = false
		result.Error = nil
		return
	}

	code := ""
	stage := ""
	if result.Error != nil {
		code = strings.TrimSpace(result.Error.Code)
		stage = strings.TrimSpace(result.Error.Stage)
	}
	code = canonicalWorkerJobErrorCode(action, code, result.TimedOut)
	stage = canonicalWorkerJobErrorStage(action, code, stage)
	result.TimedOut = code == "worker_timeout"
	result.Error = &platform.WorkerJobError{
		Code:      code,
		Stage:     stage,
		Retryable: canonicalWorkerJobErrorRetryable(code, stage),
		Details:   map[string]string{},
	}
}

func canonicalWorkerJobErrorCode(action, code string, timedOut bool) string {
	if code == "worker_timeout" || (code == "" && timedOut) {
		return "worker_timeout"
	}
	if code == "worker_failed" || code == "worker_action_unsupported" {
		return code
	}
	switch action {
	case "create-sandbox":
		switch code {
		case "sandbox_create_failed", "sandbox_create_cleanup_failed", "worker_interrupted",
			"worker_interrupted_cleanup_failed", "job_cancelled", "cancellation_cleanup_failed":
			return code
		}
	case "start-sandbox":
		if code == "start_sandbox_failed" {
			return code
		}
	case "stop-sandbox":
		if code == "stop_sandbox_failed" {
			return code
		}
	case "restart-sandbox":
		if code == "restart_sandbox_failed" {
			return code
		}
	case "delete-sandbox":
		if code == "delete_sandbox_failed" {
			return code
		}
	case "check-network-proxy":
		if code == "network_proxy_check_failed" {
			return code
		}
	case "check-sandbox-agent-tools":
		if code == "sandbox_agent_tools_check_failed" {
			return code
		}
	case "update-sandbox-agent-tools":
		if code == "sandbox_agent_tools_update_failed" {
			return code
		}
	case "configure-sandbox-proxy":
		if code == "sandbox_proxy_apply_failed" {
			return code
		}
	}
	return fallbackWorkerJobErrorCode(action)
}

func fallbackWorkerJobErrorCode(action string) string {
	switch action {
	case "create-sandbox":
		return "sandbox_create_failed"
	case "start-sandbox":
		return "start_sandbox_failed"
	case "stop-sandbox":
		return "stop_sandbox_failed"
	case "restart-sandbox":
		return "restart_sandbox_failed"
	case "delete-sandbox":
		return "delete_sandbox_failed"
	case "check-network-proxy":
		return "network_proxy_check_failed"
	case "check-sandbox-agent-tools":
		return "sandbox_agent_tools_check_failed"
	case "update-sandbox-agent-tools":
		return "sandbox_agent_tools_update_failed"
	case "configure-sandbox-proxy":
		return "sandbox_proxy_apply_failed"
	default:
		return "worker_failed"
	}
}

func canonicalWorkerJobErrorStage(action, code, stage string) string {
	switch code {
	case "job_cancelled", "cancellation_cleanup_failed":
		return "cancel"
	case "sandbox_create_cleanup_failed", "worker_interrupted_cleanup_failed":
		return "cleanup"
	case "worker_interrupted":
		return "create"
	case "worker_timeout":
		return "execution"
	case "worker_action_unsupported":
		return "dispatch"
	}
	if safeWorkerJobErrorStage(action, stage) {
		return stage
	}
	return fallbackWorkerJobErrorStage(action)
}

func safeWorkerJobErrorStage(action, stage string) bool {
	switch action {
	case "create-sandbox":
		switch stage {
		case "agent-tools", "agent-wrappers", "cleanup", "create", "create-result", "credentials",
			"desktop-config", "desktop-start", "extensions", "image-prepare", "manifest-write", "mcp",
			"network-policy", "proxy-config", "runtime-create", "runtime-image", "runtime-probe",
			"setup-command", "skills", "variables", "workspace-init":
			return true
		}
	case "start-sandbox", "restart-sandbox":
		if stage == strings.TrimSuffix(action, "-sandbox") {
			return true
		}
		switch stage {
		case "agent-tools", "agent-wrappers", "credentials", "manifest-write", "mcp", "network-policy",
			"proxy-config", "runtime-probe", "skills", "variables":
			return true
		}
	case "stop-sandbox":
		return stage == "stop"
	case "delete-sandbox":
		return stage == "delete"
	case "check-network-proxy":
		return stage == "proxy-check"
	case "check-sandbox-agent-tools", "update-sandbox-agent-tools":
		return stage == "agent-tools"
	case "configure-sandbox-proxy":
		return stage == "proxy-config"
	default:
		return strings.HasPrefix(action, "workspace-") && stage == "workspace-init"
	}
	return false
}

func fallbackWorkerJobErrorStage(action string) string {
	switch action {
	case "create-sandbox":
		return "create"
	case "start-sandbox":
		return "start"
	case "stop-sandbox":
		return "stop"
	case "restart-sandbox":
		return "restart"
	case "delete-sandbox":
		return "delete"
	case "check-network-proxy":
		return "proxy-check"
	case "check-sandbox-agent-tools", "update-sandbox-agent-tools":
		return "agent-tools"
	case "configure-sandbox-proxy":
		return "proxy-config"
	default:
		if strings.HasPrefix(action, "workspace-") {
			return "workspace-init"
		}
		return "execution"
	}
}

func canonicalWorkerJobErrorRetryable(code, stage string) bool {
	switch code {
	case "worker_timeout", "network_proxy_check_failed", "sandbox_proxy_apply_failed":
		return true
	case "sandbox_create_failed":
		switch stage {
		case "runtime-probe", "runtime-image", "image-prepare", "runtime-create":
			return true
		}
	}
	return false
}

func trustedSandboxCompletionExternalID(
	action, resourceID string,
	payloadJSON []byte,
	success bool,
) (string, error) {
	if !success || action == "delete-sandbox" {
		return "", nil
	}
	if !validSandboxResourceID(resourceID) {
		return "", fmt.Errorf("trusted sandbox job identity is invalid")
	}
	deterministic := "agentbox-" + resourceID
	if action == "create-sandbox" {
		return deterministic, nil
	}
	var payload struct {
		ExternalID string `json:"externalId"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", fmt.Errorf("decode trusted sandbox job identity: %w", err)
	}
	externalID := strings.TrimSpace(payload.ExternalID)
	if externalID == "" {
		return deterministic, nil
	}
	if !validSandboxExternalID(externalID) {
		return "", fmt.Errorf("trusted sandbox job external identity is invalid")
	}
	return externalID, nil
}

func validSandboxResourceID(id string) bool {
	if len(id) < 2 || len(id) > 64 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	previousHyphen := false
	for index := range len(id) {
		character := id[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			previousHyphen = false
			continue
		}
		if character != '-' || previousHyphen {
			return false
		}
		previousHyphen = true
	}
	return true
}

func validSandboxExternalID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for index := range len(id) {
		character := id[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_.-", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func isSandboxLifecycleAction(action string) bool {
	switch action {
	case "create-sandbox", "start-sandbox", "stop-sandbox", "restart-sandbox", "delete-sandbox":
		return true
	default:
		return false
	}
}

func isGuestInteractingWorkerAction(action string) bool {
	return strings.Contains(action, "sandbox") || strings.HasPrefix(action, "workspace-") ||
		action == "check-network-proxy"
}

func safeWorkerCompletionMessage(action string, result platform.WorkerJobResult) string {
	if installationCancelled(result) {
		return sandboxCancelledMessage
	}
	labels := map[string]string{
		"check-network-proxy":        "网络代理检测",
		"check-sandbox-agent-tools":  "Agent 工具检测",
		"configure-sandbox-proxy":    "沙箱网络出口配置",
		"create-sandbox":             "沙箱创建",
		"delete-sandbox":             "沙箱删除",
		"restart-sandbox":            "沙箱重启",
		"start-sandbox":              "沙箱启动",
		"stop-sandbox":               "沙箱停止",
		"update-sandbox-agent-tools": "Agent 工具更新",
	}
	label := labels[action]
	if label == "" {
		label = "Worker 任务"
	}
	if result.Success {
		return label + "完成"
	}
	return label + "失败"
}

func sanitizeWorkerJobProgress(action string, input *platform.WorkerJobProgressInput) {
	if input == nil || !isGuestInteractingWorkerAction(action) {
		return
	}
	if !safeWorkerProgressStage(input.Stage) {
		input.Stage = "working"
	}
	input.Message = "Worker 正在执行受保护任务"
	if input.ExtensionID != "" {
		input.Message = "沙箱扩展状态已更新"
	} else if input.AgentTool != "" {
		input.Message = "Agent 工具状态已更新"
	}
	input.CacheStatus = ""
	input.CacheReason = ""
	input.ExtensionOutput = ""
	if input.AgentTool != "" && (!safeAgentToolName(input.AgentTool) || !safeProgressAgentToolStatus(input.AgentToolStatus)) {
		input.AgentTool = ""
		input.AgentToolStatus = ""
	}
}

func safeWorkerProgressStage(stage string) bool {
	switch stage {
	case "agent-image", "agent-tools-update", "configuration", "desktop-install", "desktop-start",
		"extensions", "image-prepare", "proxy-config", "runtime-check", "runtime-create",
		"runtime-start", "setup", "verify", "working":
		return true
	default:
		return false
	}
}

func safeWorkerAgentToolStates(
	states []platform.SandboxAgentToolState,
	now time.Time,
) []platform.SandboxAgentToolState {
	result := make([]platform.SandboxAgentToolState, 0, len(states))
	for _, state := range states {
		if !safeAgentToolName(state.Tool) || !safeCompletedAgentToolStatus(state.Status) {
			continue
		}
		result = append(result, platform.SandboxAgentToolState{
			Tool: state.Tool, Status: state.Status, CheckedAt: now,
		})
	}
	return result
}

func sanitizeWorkerProvisioningProgress(action string, progress *platform.ProvisioningProgress) {
	if progress == nil || !isGuestInteractingWorkerAction(action) {
		return
	}
	if !safeWorkerProgressStage(progress.Stage) {
		progress.Stage = "working"
	}
	progress.Message = ""
	progress.CacheStatus = ""
	progress.CacheReason = ""
	for index := range progress.AgentTools {
		progress.AgentTools[index].Message = ""
	}
	for index := range progress.Extensions {
		progress.Extensions[index].Message = ""
		progress.Extensions[index].Output = ""
	}
}

func safeAgentToolName(tool string) bool {
	switch tool {
	case "claude-code", "codex", "deepseek-harness", "gemini-cli", "grok", "kimi", "opencode", "pi", "reasonix":
		return true
	default:
		return false
	}
}

func safeProgressAgentToolStatus(status string) bool {
	switch status {
	case "running", "installed", "verifying", "succeeded", "failed", "cached":
		return true
	default:
		return false
	}
}

func safeCompletedAgentToolStatus(status string) bool {
	switch status {
	case "installed", "not-installed", "broken", "updated", "unchanged", "failed":
		return true
	default:
		return false
	}
}

func canonicalNetworkProxyCheckOutput(output string, checkedAt time.Time) string {
	result := platform.NetworkProxyCheck{}
	applyNetworkProxyCheckOutput(&result, output)
	result.CheckedAt = &checkedAt
	canonical := struct {
		OK         bool      `json:"ok"`
		LatencyMS  int64     `json:"latencyMs,omitempty"`
		StatusCode int       `json:"statusCode,omitempty"`
		Error      string    `json:"error,omitempty"`
		CheckedAt  time.Time `json:"checkedAt"`
	}{
		LatencyMS: result.LatencyMS, StatusCode: result.StatusCode,
		Error: result.Error, CheckedAt: checkedAt,
	}
	if result.OK != nil {
		canonical.OK = *result.OK
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return `{"ok":false,"error":"Worker 未返回有效的代理检测结果"}`
	}
	return string(encoded)
}
