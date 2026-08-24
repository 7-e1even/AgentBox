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
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_worker_job`,
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("schema is missing %q", expected)
		}
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
