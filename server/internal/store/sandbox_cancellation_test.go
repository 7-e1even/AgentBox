package store

import (
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestCancellationProgressIgnoresLateInstallationReports(t *testing.T) {
	now := time.Now().UTC()
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "cancelling", Message: sandboxCancellationMessage,
	}, now)
	progress.CancelRequested = true
	progress.Status = "cancelling"
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "completed", Message: "late success", AgentTool: "codex", AgentToolStatus: "succeeded",
	}, now.Add(time.Second))
	if progress.Status != "cancelling" || progress.Stage != "cancelling" || progress.Message != sandboxCancellationMessage || len(progress.AgentTools) != 0 {
		t.Fatalf("late progress changed cancellation: %+v", progress)
	}
	if progress.FinishedAt != nil {
		t.Fatal("cancellation was marked finished before Worker acknowledgement")
	}
}

func TestCancelledProgressClosesActiveToolsAndExtensions(t *testing.T) {
	now := time.Now().UTC()
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "agent-image", AgentTool: "reasonix", AgentToolStatus: "running",
	}, now)
	progress.Extensions = []platform.ProvisioningExtension{{ID: "extension", Status: "installing", StartedAt: &now}}
	progress.CancelRequested = true
	progress = finishProvisioningProgress(progress, platform.WorkerJobResult{
		Message: sandboxCancelledMessage, Error: &platform.WorkerJobError{Code: "job_cancelled"},
	}, now.Add(time.Second))
	if progress.Status != "cancelled" || progress.Stage != "cancelled" || progress.FinishedAt == nil ||
		progress.AgentTools[0].Status != "cancelled" || progress.Extensions[0].Status != "cancelled" {
		t.Fatalf("cancelled progress = %+v", progress)
	}
}
