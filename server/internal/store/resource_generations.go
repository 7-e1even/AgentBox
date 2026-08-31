package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Lock the resource until the job transaction commits, so a configuration
// mutation cannot race the generation check and the subsequent status write.
func ensureWorkerResourceGeneration(ctx context.Context, tx pgx.Tx, id string, generation int64) error {
	var current int64
	err := tx.QueryRow(ctx, `SELECT generation FROM control_resources WHERE id = $1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current != generation) {
		return fmt.Errorf("%w: resource configuration changed since the job was queued", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("check Worker resource generation: %w", err)
	}
	return nil
}

func failStaleResourceJob(ctx context.Context, tx pgx.Tx, jobID string) error {
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET status = 'failed', lease_until = NULL,
    result_error_code = 'resource_generation_changed', result_error_retryable = FALSE,
    result_message = '资源配置版本已变化，旧任务结果未应用', updated_at = NOW() WHERE id = $1`, jobID); err != nil {
		return fmt.Errorf("retire stale resource job: %w", err)
	}
	_, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'failed',
    error_code = 'resource_generation_changed', error_retryable = FALSE,
    error_message = '资源配置版本已变化，旧任务结果未应用', finished_at = NOW()
    WHERE worker_job_id = $1 AND status IN ('queued', 'provisioning')`, jobID)
	return err
}
