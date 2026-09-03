package store

import (
	"errors"
	"maps"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func newVersionedSandbox(t *testing.T) (*Store, string, string, platform.Resource) {
	t.Helper()
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatal(err)
	}
	inventory := platform.ServerInventory{DockerImages: []platform.ServerImage{{Reference: "ubuntu:24.04", Architecture: "amd64"}}}
	if err := s.HeartbeatServer(ctx, serverID, credential, []string{"docker"}, &inventory, "test"); err != nil {
		t.Fatal(err)
	}
	projectID := "default"
	runtimeInput := platform.Input{ID: "version-template", Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "Version template", Enabled: true, Spec: map[string]any{
			"serverId": serverID, "driver": "docker", "imageReference": "ubuntu:24.04", "desktop": true,
		}}
	if _, err := s.CreateResource(ctx, runtimeInput); err != nil {
		t.Fatal(err)
	}
	sandbox, err := s.CreateResource(ctx, platform.Input{ID: "version-sandbox", Kind: platform.KindSandbox,
		ProjectID: &projectID, Name: "Version sandbox", Enabled: true, Spec: map[string]any{
			"serverId": serverID, "runtimeId": runtimeInput.ID, "desktop": true, "status": "forged-status",
		}})
	if err != nil {
		t.Fatal(err)
	}
	return s, serverID, credential, sandbox
}

func TestResourceGenerationsTrackDesiredAndObservedState(t *testing.T) {
	s, serverID, credential, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	if sandbox.SpecVersion != 1 || sandbox.Generation != 1 || sandbox.ObservedGeneration != 0 || sandbox.Spec["status"] != "requested" {
		t.Fatalf("new resource = %#v", sandbox)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil || job.ResourceGeneration != 1 {
		t.Fatalf("job = %#v, error = %v", job, err)
	}
	firstTokenEpoch := job.ID
	if job.Payload["runtimeTokenEpoch"] != firstTokenEpoch {
		t.Fatalf("create job token epoch = %#v, want %q", job.Payload["runtimeTokenEpoch"], firstTokenEpoch)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "version-container",
	}); err != nil {
		t.Fatal(err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil || sandbox.ObservedGeneration != 1 {
		t.Fatalf("completed resource = %#v, error = %v", sandbox, err)
	}
	if sandbox.Spec["runtimeModelTokenEpoch"] != firstTokenEpoch {
		t.Fatalf("created sandbox token epoch = %#v, want %q", sandbox.Spec["runtimeModelTokenEpoch"], firstTokenEpoch)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec ||
    '{"automationRunId":"original-run","agentToolVersions":[{"tool":"codex","status":"ready"}]}'::jsonb WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	input := sandbox.Input
	input.Name = "Updated sandbox"
	input.Spec["automationRunId"] = "forged-run"
	input.Spec["agentToolVersions"] = []any{}
	updated, err := s.UpdateResource(ctx, sandbox.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 2 || updated.ObservedGeneration != 1 || updated.Spec["automationRunId"] != "original-run" ||
		len(updated.Spec["agentToolVersions"].([]any)) != 1 || updated.Spec["runtimeModelTokenEpoch"] != firstTokenEpoch {
		t.Fatalf("configuration update lost observations: %#v", updated)
	}
	if _, err := s.OperateSandbox(ctx, sandbox.ID, "restart"); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil || job.ResourceGeneration != 2 {
		t.Fatalf("new configuration job = %#v, error = %v", job, err)
	}
	if job.ID == firstTokenEpoch || job.Payload["runtimeTokenEpoch"] != job.ID {
		t.Fatalf("restart job token epoch = %#v for job %q", job.Payload["runtimeTokenEpoch"], job.ID)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{LeaseGeneration: job.LeaseGeneration, Success: true}); err != nil {
		t.Fatal(err)
	}
	updated, err = s.GetResource(ctx, sandbox.ID)
	if err != nil || updated.ObservedGeneration != 2 {
		t.Fatalf("observed = %d, error = %v", updated.ObservedGeneration, err)
	}
	if updated.Spec["runtimeModelTokenEpoch"] != job.ID {
		t.Fatalf("restarted sandbox token epoch = %#v, want %q", updated.Spec["runtimeModelTokenEpoch"], job.ID)
	}
}

func TestResourceGenerationFencesJanitorStatusUpdates(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "current generation expires"
		if changed {
			name = "old generation cannot replace current status"
		}
		t.Run(name, func(t *testing.T) {
			s, serverID, _, sandbox := newVersionedSandbox(t)
			ctx := t.Context()
			if _, err := s.pool.Exec(ctx, `UPDATE managed_servers SET
    created_at = NOW() - INTERVAL '1 hour', last_seen_at = NOW() - INTERVAL '1 hour',
    last_job_activity_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, serverID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET created_at = NOW() - INTERVAL '1 hour'
    WHERE resource_id = $1`, sandbox.ID); err != nil {
				t.Fatal(err)
			}
			if changed {
				if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET generation = 2,
    spec = spec || '{"message":"new configuration"}'::jsonb WHERE id = $1`, sandbox.ID); err != nil {
					t.Fatal(err)
				}
			}
			s.runJanitorPass(ctx)
			current, err := s.GetResource(ctx, sandbox.ID)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				if current.Spec["status"] != "requested" || current.Spec["message"] != "new configuration" {
					t.Fatalf("janitor overwrote new configuration: %#v", current)
				}
			} else if current.Spec["status"] != "error" {
				t.Fatalf("expired creation status = %v, want error", current.Spec["status"])
			}
		})
	}
}

func TestResourceGenerationFencesOldProgressAndCompletion(t *testing.T) {
	s, serverID, credential, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a newer configuration even though the public mutation API refuses
	// edits during active jobs. Fencing must remain correct independently.
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET generation = generation + 1,
    spec = spec || '{"desktop":false,"message":"new configuration"}'::jsonb WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, job.ID, platform.WorkerJobProgressInput{
		LeaseGeneration: job.LeaseGeneration, Stage: "old-stage", Message: "old progress",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale progress = %v, want conflict", err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "old-container", Message: "old completion",
	}); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetResource(ctx, sandbox.ID)
	if err != nil || current.Spec["desktop"] != false || current.Spec["message"] != "new configuration" || current.ObservedGeneration != 0 {
		t.Fatalf("old job overwrote current resource: %#v, error %v", current, err)
	}
	var status, code string
	if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code FROM worker_jobs WHERE id = $1`, job.ID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "resource_generation_changed" {
		t.Fatalf("old job = %s/%s", status, code)
	}
}

func TestActiveJobBlocksConfigurationChangesAndStalePendingJobIsNotClaimed(t *testing.T) {
	s, serverID, credential, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	if _, err := s.UpdateResource(ctx, sandbox.ID, sandbox.Input); !errors.Is(err, ErrConflict) {
		t.Fatalf("active configuration update = %v, want conflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET generation = 2 WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWorkerJob(ctx, serverID, credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("stale pending claim = %v, want no job", err)
	}
	var pending int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM worker_jobs WHERE resource_id = $1 AND status IN ('pending','leased')`, sandbox.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("stale pending jobs = %d, error = %v", pending, err)
	}
}

func TestResourceQueriesFilterInDatabase(t *testing.T) {
	s, _, _, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	resources, err := s.ListResourcesFiltered(ctx, platform.ResourceFilter{Kind: platform.KindRuntime, ProjectID: "default"})
	if err != nil || len(resources) != 1 || resources[0].Kind != platform.KindRuntime {
		t.Fatalf("filtered resources = %#v, error = %v", resources, err)
	}
	resources, err = s.ListResourcesFiltered(ctx, platform.ResourceFilter{ProjectID: "missing-project"})
	if err != nil || len(resources) != 0 {
		t.Fatalf("other project resources = %#v, error = %v", resources, err)
	}
	if _, err := s.GetResource(ctx, "missing-resource"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing resource = %v", err)
	}
	if _, err := s.ListResourcesFiltered(ctx, platform.ResourceFilter{Kind: "unknown"}); !platform.IsValidationError(err) {
		t.Fatalf("invalid filter = %v", err)
	}
	if got, err := s.GetResource(ctx, sandbox.ID); err != nil || got.SpecVersion != 1 {
		t.Fatalf("resource = %#v, error = %v", got, err)
	}
}

func TestSandboxCreationFieldsStayFrozenAcrossEditsAndTemplateChanges(t *testing.T) {
	s, serverID, credential, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "frozen-container",
	}); err != nil {
		t.Fatal(err)
	}
	for _, field := range sandboxCreationFields {
		t.Run(field, func(t *testing.T) {
			input := sandbox.Input
			input.Spec = maps.Clone(input.Spec)
			input.Spec[field] = "changed"
			if field == "desktop" {
				input.Spec[field] = false
			}
			if _, err := s.UpdateResource(ctx, sandbox.ID, input); !platform.IsValidationError(err) {
				t.Fatalf("changing creation field %s = %v, want validation error", field, err)
			}
		})
	}
	template, err := s.GetResource(ctx, "version-template")
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["cpu"] = "4"
	template.Spec["memory"] = "8 GiB"
	template.Spec["desktop"] = false
	if _, err := s.UpdateResource(ctx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperateSandbox(ctx, sandbox.ID, "restart"); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if job.Payload["cpu"] != sandbox.Spec["cpu"] || job.Payload["memory"] != sandbox.Spec["memory"] || job.Payload["desktop"] != true {
		t.Fatalf("template changed existing instance configuration: %#v", job.Payload)
	}
}
