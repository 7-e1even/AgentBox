package store

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestSchemaPersistsWorkerAndAutomationProvisioningProgress(t *testing.T) {
	for _, expected := range []string{
		`progress JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`provisioning JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS progress`,
		`ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS provisioning`,
		`result_error_code TEXT NOT NULL DEFAULT ''`,
		`result_error_details JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_worker_job`,
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("schema is missing %q", expected)
		}
	}
}

func TestNormalizedWorkerJobErrorKeepsStructuredFailureAndLegacyFallback(t *testing.T) {
	structured := normalizedWorkerJobError(platform.WorkerJobResult{Error: &platform.WorkerJobError{
		Code: "sandbox_create_failed", Stage: "mcp", Retryable: false,
		Details: map[string]string{"action": "create-sandbox"},
	}})
	if structured.Code != "sandbox_create_failed" || structured.Stage != "mcp" ||
		structured.Details["action"] != "create-sandbox" {
		t.Fatalf("structured Worker error = %#v", structured)
	}

	legacy := normalizedWorkerJobError(platform.WorkerJobResult{TimedOut: true})
	if legacy.Code != "worker_timeout" || legacy.Stage != "execution" || !legacy.Retryable {
		t.Fatalf("legacy timed-out Worker error = %#v", legacy)
	}
}

func TestProvisioningProgressRecordsStageTimingsAndCacheResult(t *testing.T) {
	startedAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "runtime-check", Message: "正在检查运行时",
	}, startedAt)
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "命中 Agent 工具缓存",
		CacheStatus: "hit", CacheReason: "exact-cache",
	}, startedAt.Add(1500*time.Millisecond))

	if len(progress.Timings) != 1 {
		t.Fatalf("timings = %d, want 1", len(progress.Timings))
	}
	if got := progress.Timings[0].DurationMS; got != 1500 {
		t.Fatalf("runtime-check duration = %d, want 1500", got)
	}
	if progress.CacheStatus != "hit" || progress.CacheReason != "exact-cache" {
		t.Fatalf("cache result = %q/%q", progress.CacheStatus, progress.CacheReason)
	}

	finished := finishProvisioningProgress(progress, platform.WorkerJobResult{
		Success: true, Message: "Sandbox created",
	}, startedAt.Add(4*time.Second))
	if finished.Status != "succeeded" || finished.Stage != "completed" {
		t.Fatalf("finished status = %q/%q", finished.Status, finished.Stage)
	}
	if finished.DurationMS != 4000 || len(finished.Timings) != 2 {
		t.Fatalf("finished duration/timings = %d/%d", finished.DurationMS, len(finished.Timings))
	}
}

func TestProvisioningHeartbeatDoesNotRestartCurrentStage(t *testing.T) {
	startedAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "正在构建缓存",
	}, startedAt)
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "正在安装 Codex",
	}, startedAt.Add(time.Minute))

	if len(progress.Timings) != 0 {
		t.Fatalf("same-stage heartbeat recorded %d completed timings", len(progress.Timings))
	}
	if progress.StageStartedAt == nil || !progress.StageStartedAt.Equal(startedAt) {
		t.Fatalf("stage start changed to %v", progress.StageStartedAt)
	}
}

func TestProvisioningProgressTracksAgentToolStatusAndDuration(t *testing.T) {
	startedAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "正在安装 Codex",
		AgentTool: "codex", AgentToolStatus: "running",
	}, startedAt)
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "Codex 已安装，等待验证",
		AgentTool: "codex", AgentToolStatus: "installed",
	}, startedAt.Add(2*time.Second))
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "正在验证 Codex",
		AgentTool: "codex", AgentToolStatus: "verifying",
	}, startedAt.Add(3*time.Second))
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "Codex 已就绪",
		AgentTool: "codex", AgentToolStatus: "succeeded",
	}, startedAt.Add(4*time.Second))
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "已复用 Grok 缓存",
		AgentTool: "grok", AgentToolStatus: "cached",
	}, startedAt.Add(5*time.Second))

	if len(progress.AgentTools) != 2 {
		t.Fatalf("agent tool progress = %d, want 2", len(progress.AgentTools))
	}
	if codex := progress.AgentTools[0]; codex.Status != "succeeded" || codex.DurationMS != 4000 || codex.FinishedAt == nil {
		t.Fatalf("codex progress = %+v", codex)
	}
	if grok := progress.AgentTools[1]; grok.Status != "cached" || grok.DurationMS != 0 || grok.FinishedAt == nil {
		t.Fatalf("grok progress = %+v", grok)
	}
}

func TestProvisioningFailureClosesRunningAgentTool(t *testing.T) {
	startedAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "agent-image", Message: "正在安装 Grok",
		AgentTool: "grok", AgentToolStatus: "running",
	}, startedAt)
	progress = finishProvisioningProgress(progress, platform.WorkerJobResult{
		Success: false, Message: "Grok 下载失败",
	}, startedAt.Add(5*time.Second))

	tool := progress.AgentTools[0]
	if tool.Status != "failed" || tool.DurationMS != 5000 || tool.Message != "Grok 下载失败" {
		t.Fatalf("grok failure progress = %+v", tool)
	}
}
