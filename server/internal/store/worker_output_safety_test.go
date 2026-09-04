package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestSanitizeWorkerJobCompletionIgnoresUntrustedExternalIDAndOutput(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	secret := "secret-like-external-id"
	result := platform.WorkerJobResult{
		Success: true, ExternalID: secret, Message: secret, Output: secret,
		OutputTruncated: true,
	}
	if err := sanitizeWorkerJobCompletion(
		"create-sandbox", "safe-sandbox", []byte(`{"sandboxId":"safe-sandbox"}`), &result, now,
	); err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "agentbox-safe-sandbox" || result.Message != "沙箱创建完成" ||
		result.Output != "" || result.OutputTruncated {
		t.Fatalf("sanitized result = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("sanitized result retained untrusted data: %s", encoded)
	}
}

func TestTrustedSandboxCompletionExternalIDUsesPayloadOrDeterministicFallback(t *testing.T) {
	for _, test := range []struct {
		name     string
		action   string
		payload  string
		success  bool
		expected string
	}{
		{name: "create", action: "create-sandbox", success: true, expected: "agentbox-safe-sandbox"},
		{name: "existing instance", action: "restart-sandbox", payload: `{"externalId":"existing.instance"}`, success: true, expected: "existing.instance"},
		{name: "legacy fallback", action: "start-sandbox", payload: `{}`, success: true, expected: "agentbox-safe-sandbox"},
		{name: "failed completion", action: "stop-sandbox", payload: `{"externalId":"existing.instance"}`},
		{name: "delete", action: "delete-sandbox", payload: `{"externalId":"existing.instance"}`, success: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			externalID, err := trustedSandboxCompletionExternalID(
				test.action, "safe-sandbox", []byte(test.payload), test.success,
			)
			if err != nil {
				t.Fatal(err)
			}
			if externalID != test.expected {
				t.Fatalf("external ID = %q, want %q", externalID, test.expected)
			}
		})
	}
}

func TestSanitizeWorkerJobCompletionCanonicalizesProxyCheckAndAgentTools(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	secret := "proxy-check-secret"
	proxyResult := platform.WorkerJobResult{
		Success: true, Message: secret,
		Output: `{"ok":true,"latencyMs":12,"statusCode":204,"target":"` + secret + `","unknown":"` + secret + `"}`,
	}
	if err := sanitizeWorkerJobCompletion("check-network-proxy", "", nil, &proxyResult, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(proxyResult.Output, secret) || proxyResult.Message != "网络代理检测完成" {
		t.Fatalf("canonical proxy result = %#v", proxyResult)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(proxyResult.Output), &output); err != nil {
		t.Fatal(err)
	}
	if output["ok"] != true || output["statusCode"] != float64(204) || output["checkedAt"] == nil {
		t.Fatalf("canonical proxy output = %#v", output)
	}

	agentResult := platform.WorkerJobResult{Success: true, AgentTools: []platform.SandboxAgentToolState{{
		Tool: "codex", Status: "updated", CurrentVersion: secret, LatestVersion: secret,
		PreviousVersion: secret, Message: secret, Source: secret, CheckedAt: now.Add(-time.Hour),
	}}}
	if err := sanitizeWorkerJobCompletion("update-sandbox-agent-tools", "safe-sandbox", nil, &agentResult, now); err != nil {
		t.Fatal(err)
	}
	if len(agentResult.AgentTools) != 1 || agentResult.AgentTools[0].CurrentVersion != "" ||
		agentResult.AgentTools[0].LatestVersion != "" || agentResult.AgentTools[0].PreviousVersion != "" ||
		agentResult.AgentTools[0].Message != "" || agentResult.AgentTools[0].Source != "" ||
		!agentResult.AgentTools[0].CheckedAt.Equal(now) {
		t.Fatalf("sanitized Agent tool result = %#v", agentResult.AgentTools)
	}
}

func TestSanitizeWorkerJobCompletionCanonicalizesErrorMetadata(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	secret := "stage-secrettoken123-failed"
	for _, test := range []struct {
		name          string
		action        string
		result        platform.WorkerJobResult
		wantCode      string
		wantStage     string
		wantRetryable bool
		wantTimedOut  bool
		wantCancelled bool
	}{
		{
			name:   "unknown lifecycle metadata falls back to its trusted action",
			action: "start-sandbox",
			result: platform.WorkerJobResult{Error: &platform.WorkerJobError{
				Code: secret, Stage: secret, Retryable: true, Details: map[string]string{"raw": secret},
			}},
			wantCode: "start_sandbox_failed", wantStage: "start",
		},
		{
			name:   "known transient create stage derives retryability",
			action: "create-sandbox",
			result: platform.WorkerJobResult{Error: &platform.WorkerJobError{
				Code: "sandbox_create_failed", Stage: "runtime-probe",
			}},
			wantCode: "sandbox_create_failed", wantStage: "runtime-probe", wantRetryable: true,
		},
		{
			name:   "cancellation cleanup keeps transaction semantics",
			action: "create-sandbox",
			result: platform.WorkerJobResult{TimedOut: true, Error: &platform.WorkerJobError{
				Code: "cancellation_cleanup_failed", Stage: secret, Retryable: true,
			}},
			wantCode: "cancellation_cleanup_failed", wantStage: "cancel",
		},
		{
			name:   "cancelled create remains recognisable",
			action: "create-sandbox",
			result: platform.WorkerJobResult{Error: &platform.WorkerJobError{
				Code: "job_cancelled", Stage: secret, Retryable: true,
			}},
			wantCode: "job_cancelled", wantStage: "cancel", wantCancelled: true,
		},
		{
			name:   "proxy failure uses fixed stage and remains retryable",
			action: "check-network-proxy",
			result: platform.WorkerJobResult{Error: &platform.WorkerJobError{
				Code: secret, Stage: secret,
			}},
			wantCode: "network_proxy_check_failed", wantStage: "proxy-check", wantRetryable: true,
		},
		{
			name:   "explicit timeout has fixed metadata",
			action: "restart-sandbox",
			result: platform.WorkerJobResult{Error: &platform.WorkerJobError{
				Code: "worker_timeout", Stage: secret,
			}},
			wantCode: "worker_timeout", wantStage: "execution", wantRetryable: true, wantTimedOut: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := test.result
			if err := sanitizeWorkerJobCompletion(test.action, "safe-sandbox", nil, &result, now); err != nil {
				t.Fatal(err)
			}
			if result.Error == nil || result.Error.Code != test.wantCode || result.Error.Stage != test.wantStage ||
				result.Error.Retryable != test.wantRetryable || result.TimedOut != test.wantTimedOut ||
				installationCancelled(result) != test.wantCancelled || len(result.Error.Details) != 0 {
				t.Fatalf("canonical result = %#v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("canonical result retained guest metadata: %s", encoded)
			}
		})
	}

	success := platform.WorkerJobResult{
		Success: true, TimedOut: true,
		Error: &platform.WorkerJobError{Code: secret, Stage: secret, Retryable: true},
	}
	if err := sanitizeWorkerJobCompletion("restart-sandbox", "safe-sandbox", []byte(`{}`), &success, now); err != nil {
		t.Fatal(err)
	}
	if success.Error != nil || success.TimedOut {
		t.Fatalf("successful result retained contradictory error metadata: %#v", success)
	}
}

func TestSanitizeWorkerJobProgressRemovesGuestText(t *testing.T) {
	secret := "extension-progress-secret"
	input := platform.WorkerJobProgressInput{
		Stage: "secret-stage", Message: secret, CacheStatus: secret, CacheReason: secret,
		ExtensionID: "safe-extension", ExtensionStatus: "succeeded", ExtensionOutput: secret,
	}
	sanitizeWorkerJobProgress("create-sandbox", &input)
	if input.Stage != "working" || input.Message != "沙箱扩展状态已更新" ||
		input.CacheStatus != "" || input.CacheReason != "" || input.ExtensionOutput != "" {
		t.Fatalf("sanitized progress = %#v", input)
	}
}
