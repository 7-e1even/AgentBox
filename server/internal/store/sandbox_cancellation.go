package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const sandboxCancellationMessage = "正在取消安装，等待 Worker 停止进程并清理资源"
const sandboxCancelledMessage = "已取消安装，可删除此沙箱后重新创建"

// ControlWorkerJob both advertises this executor's cancellation support and
// renews its exact lease. A cancelled lease is never reassigned, so its owner
// may reconnect after expiry to finish cleanup.
func (s *Store) ControlWorkerJob(ctx context.Context, serverID, credential, jobID string, input platform.WorkerJobControlInput) (platform.WorkerJobControl, error) {
	if _, err := uuid.Parse(serverID); err != nil || len(credential) < 32 {
		return platform.WorkerJobControl{}, ErrWorkerUnauthorized
	}
	if _, err := uuid.Parse(jobID); err != nil || input.LeaseGeneration < 1 {
		return platform.WorkerJobControl{}, ErrResourceNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.WorkerJobControl{}, fmt.Errorf("begin worker job control: %w", err)
	}
	defer tx.Rollback(ctx)
	var authenticated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM managed_servers
      WHERE id = $1 AND credential_hash = $2)`, serverID, hashToken(credential)).Scan(&authenticated); err != nil {
		return platform.WorkerJobControl{}, fmt.Errorf("authenticate worker job control: %w", err)
	}
	if !authenticated {
		return platform.WorkerJobControl{}, ErrWorkerUnauthorized
	}
	now := time.Now().UTC()
	var control platform.WorkerJobControl
	var resourceID string
	var generation int64
	var progressJSON []byte
	err = tx.QueryRow(ctx, `UPDATE worker_jobs SET
      progress = progress || '{"cancellationSupported":true}'::jsonb,
      lease_until = $1, updated_at = $2
      WHERE id = $3 AND server_id = $4 AND action = 'create-sandbox'
        AND status = 'leased' AND attempts = $5
        AND (lease_until >= $2 OR cancel_requested_at IS NOT NULL)
      RETURNING resource_id, resource_generation, progress, cancel_requested_at IS NOT NULL`,
		now.Add(workerJobLeaseDurationForAction("create-sandbox")), now,
		jobID, serverID, input.LeaseGeneration).Scan(&resourceID, &generation, &progressJSON, &control.CancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.WorkerJobControl{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.WorkerJobControl{}, fmt.Errorf("control worker job: %w", err)
	}
	if err := ensureWorkerResourceGeneration(ctx, tx, resourceID, generation); err != nil {
		return platform.WorkerJobControl{}, err
	}
	if err := touchWorkerJobActivity(ctx, tx, serverID, now); err != nil {
		return platform.WorkerJobControl{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET
      spec = jsonb_set(spec, '{provisioning}', $1::jsonb, true), updated_at = $2
      WHERE id = $3 AND kind = 'sandbox'`, progressJSON, now, resourceID); err != nil {
		return platform.WorkerJobControl{}, fmt.Errorf("advertise sandbox cancellation support: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.WorkerJobControl{}, fmt.Errorf("commit worker job control: %w", err)
	}
	return control, nil
}

func (s *Store) cancelSandboxInstallation(ctx context.Context, id string) (platform.Resource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Resource{}, fmt.Errorf("begin sandbox installation cancellation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneReferences(ctx, tx); err != nil {
		return platform.Resource{}, err
	}
	// Match Worker progress/completion's job-before-resource lock order.
	var jobID, status string
	var generation int64
	var progressJSON []byte
	err = tx.QueryRow(ctx, `SELECT id, status, resource_generation, progress FROM worker_jobs
      WHERE resource_id = $1 AND action = 'create-sandbox' AND status IN ('pending', 'leased')
      ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, id).Scan(&jobID, &status, &generation, &progressJSON)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return platform.Resource{}, fmt.Errorf("load sandbox installation job: %w", err)
	}
	resource, resourceErr := scanResource(tx.QueryRow(ctx, `SELECT `+resourceColumns+` FROM control_resources
      WHERE id = $1 AND kind = 'sandbox' FOR UPDATE`, id))
	if resourceErr != nil {
		if errors.Is(resourceErr, pgx.ErrNoRows) {
			return platform.Resource{}, ErrResourceNotFound
		}
		return platform.Resource{}, fmt.Errorf("load sandbox for cancellation: %w", resourceErr)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if resource.Spec["status"] == "cancelled" {
			maskResourceEnvironmentVariables(&resource)
			return resource, nil
		}
		return platform.Resource{}, &platform.ValidationError{Message: "沙箱没有正在进行的安装任务"}
	}
	if resource.Generation != generation {
		return platform.Resource{}, ErrConflict
	}
	var progress platform.ProvisioningProgress
	if err := json.Unmarshal(progressJSON, &progress); err != nil {
		return platform.Resource{}, fmt.Errorf("decode cancelled installation progress: %w", err)
	}
	if status == "leased" && !progress.CancellationSupported {
		return platform.Resource{}, &platform.ValidationError{Message: "当前 Worker 不支持取消安装，请先更新 Worker；本次安装仍在继续"}
	}
	now := time.Now().UTC()
	if !progress.CancelRequested {
		progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
			Stage: "cancelling", Message: sandboxCancellationMessage,
		}, now)
		progress.CancelRequested = true
		progress.Status = "cancelling"
	}
	sandboxStatus, message := "cancelling", sandboxCancellationMessage
	if status == "pending" {
		progress = finishProvisioningProgress(progress, platform.WorkerJobResult{
			Message: sandboxCancelledMessage,
			Error:   &platform.WorkerJobError{Code: "job_cancelled", Stage: "cancel"},
		}, now)
		sandboxStatus, message = "cancelled", sandboxCancelledMessage
	}
	progressJSON, err = json.Marshal(progress)
	if err != nil {
		return platform.Resource{}, fmt.Errorf("encode cancelled installation progress: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET
      cancel_requested_at = COALESCE(cancel_requested_at, $1), progress = $2, updated_at = $1,
      status = CASE WHEN status = 'pending' THEN 'failed' ELSE status END,
      result_message = CASE WHEN status = 'pending' THEN $3 ELSE result_message END,
      result_error_code = CASE WHEN status = 'pending' THEN 'job_cancelled' ELSE result_error_code END,
      result_error_stage = CASE WHEN status = 'pending' THEN 'cancel' ELSE result_error_stage END,
      result_error_retryable = FALSE WHERE id = $4`, now, progressJSON, message, jobID); err != nil {
		return platform.Resource{}, fmt.Errorf("request installation cancellation: %w", err)
	}
	resource, err = scanResource(tx.QueryRow(ctx, `UPDATE control_resources SET
      spec = spec || jsonb_build_object('status', $1::text, 'message', $2::text, 'provisioning', $3::jsonb),
      updated_at = $4 WHERE id = $5 RETURNING `+resourceColumns,
		sandboxStatus, message, progressJSON, now, id))
	if err != nil {
		return platform.Resource{}, fmt.Errorf("update cancelled sandbox: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET provisioning = $1,
      status = CASE WHEN $2::boolean THEN 'failed' ELSE status END,
      error_code = CASE WHEN $2::boolean THEN 'job_cancelled' ELSE error_code END,
      error_stage = CASE WHEN $2::boolean THEN 'cancel' ELSE error_stage END,
      error_message = CASE WHEN $2::boolean THEN $3 ELSE error_message END,
      error_retryable = FALSE, finished_at = CASE WHEN $2::boolean THEN $4 ELSE finished_at END
      WHERE worker_job_id = $5 AND status IN ('queued', 'provisioning')`,
		progressJSON, status == "pending", message, now, jobID); err != nil {
		return platform.Resource{}, fmt.Errorf("update cancelled automation run: %w", err)
	}
	if err := s.commitAudit(ctx, tx, platform.LogEntry{
		Category: platform.LogCategorySandbox, Action: "cancel-install", ResourceKind: "sandbox", ResourceID: id,
		Detail: map[string]any{"jobId": jobID, "status": sandboxStatus},
	}); err != nil {
		return platform.Resource{}, fmt.Errorf("commit installation cancellation: %w", err)
	}
	maskResourceEnvironmentVariables(&resource)
	return resource, nil
}

func installationCancelled(result platform.WorkerJobResult) bool {
	return !result.Success && result.Error != nil && result.Error.Code == "job_cancelled"
}
