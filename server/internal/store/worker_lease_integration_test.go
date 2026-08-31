package store

import (
	"errors"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestWorkerLeaseGenerationFencesStaleProgressAndCompletion(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}

	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, 'check-network-proxy', 'pending', '{}'::jsonb, NOW(), NOW())`,
		jobID, serverID); err != nil {
		t.Fatalf("insert Worker job: %v", err)
	}

	first, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatalf("claim first lease: %v", err)
	}
	if first.LeaseGeneration != 1 || first.MaxAttempts != workerJobAutomaticRetryLimit || first.LeaseExpiresAt.IsZero() {
		t.Fatalf("first lease = %#v", first)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET lease_until = NOW() - INTERVAL '1 minute'
	  WHERE id = $1`, jobID); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}

	second, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatalf("claim second lease: %v", err)
	}
	if second.LeaseGeneration != 2 {
		t.Fatalf("second lease generation = %d, want 2", second.LeaseGeneration)
	}

	oldProgress := platform.WorkerJobProgressInput{
		LeaseGeneration: first.LeaseGeneration,
		Stage:           "proxy-check",
		Message:         "stale executor",
	}
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, jobID, oldProgress); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("stale progress error = %v, want ErrResourceNotFound", err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, jobID, platform.WorkerJobResult{
		LeaseGeneration: first.LeaseGeneration,
		Success:         true,
		Message:         "stale executor",
	}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("stale completion error = %v, want ErrResourceNotFound", err)
	}

	currentProgress := oldProgress
	currentProgress.LeaseGeneration = second.LeaseGeneration
	currentProgress.Message = "current executor"
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, jobID, currentProgress); err != nil {
		t.Fatalf("current progress: %v", err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, jobID, platform.WorkerJobResult{
		LeaseGeneration: second.LeaseGeneration,
		Success:         true,
		Message:         "current executor",
	}); err != nil {
		t.Fatalf("current completion: %v", err)
	}

	var status string
	var attempts int
	if err := s.pool.QueryRow(ctx, `SELECT status, attempts FROM worker_jobs WHERE id = $1`, jobID).Scan(
		&status, &attempts,
	); err != nil {
		t.Fatalf("load completed job: %v", err)
	}
	if status != "succeeded" || attempts != 2 {
		t.Fatalf("completed job status = %q attempts = %d", status, attempts)
	}
}

func TestFirstLeaseKeepsControlledLegacyWorkerCompatibility(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}
	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, 'check-network-proxy', 'pending', '{}'::jsonb, NOW(), NOW())`,
		jobID, serverID); err != nil {
		t.Fatalf("insert Worker job: %v", err)
	}
	if _, err := s.ClaimWorkerJob(ctx, serverID, credential); err != nil {
		t.Fatalf("claim first lease: %v", err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, jobID, platform.WorkerJobResult{
		Success: true,
		Message: "legacy Worker without leaseGeneration",
	}); err != nil {
		t.Fatalf("complete first lease without generation: %v", err)
	}
}

func TestExpiredWorkerJobsOnlyReplayIdempotentChecksWithinLimit(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}

	nonIdempotentJobID := uuid.NewString()
	retryableJobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, lease_until, attempts, created_at, updated_at)
	  VALUES
	    ($1, $3, 'start-sandbox', 'leased', '{}'::jsonb,
	     NOW() - INTERVAL '1 minute', 1, NOW() - INTERVAL '2 minutes', NOW()),
	    ($2, $3, 'check-network-proxy', 'leased', '{}'::jsonb,
	     NOW() - INTERVAL '1 minute', 2, NOW() - INTERVAL '1 minute', NOW())`,
		nonIdempotentJobID, retryableJobID, serverID); err != nil {
		t.Fatalf("insert expired Worker jobs: %v", err)
	}

	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatalf("reclaim idempotent check: %v", err)
	}
	if job.ID != retryableJobID || job.LeaseGeneration != workerJobAutomaticRetryLimit {
		t.Fatalf("reclaimed job = %#v", job)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET lease_until = NOW() - INTERVAL '1 minute'
	  WHERE id = $1`, retryableJobID); err != nil {
		t.Fatalf("expire final retry: %v", err)
	}
	if _, err := s.ClaimWorkerJob(ctx, serverID, credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("claim after retry limit: err = %v, want ErrNoJob", err)
	}

	s.runJanitorPass(ctx)
	for _, expected := range []struct {
		id   string
		code string
	}{
		{nonIdempotentJobID, "worker_lease_expired"},
		{retryableJobID, "worker_retry_exhausted"},
	} {
		var status, code string
		if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code FROM worker_jobs WHERE id = $1`, expected.id).Scan(
			&status, &code,
		); err != nil {
			t.Fatalf("load expired job %s: %v", expected.id, err)
		}
		if status != "failed" || code != expected.code {
			t.Fatalf("expired job %s status = %q code = %q, want failed/%s", expected.id, status, code, expected.code)
		}
	}
}

func TestExpiredRetryableWorkerJobFailsWhenJobLoopIsInactive(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}
	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, lease_until, attempts, created_at, updated_at)
	  VALUES ($1, $2, 'check-network-proxy', 'leased', '{}'::jsonb,
	    NOW() - INTERVAL '1 minute', 1, NOW() - INTERVAL '2 hours', NOW())`,
		jobID, serverID); err != nil {
		t.Fatalf("insert expired Worker job: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_servers SET last_seen_at = NOW(),
	  last_job_activity_at = NOW() - INTERVAL '16 minutes' WHERE id = $1`, serverID); err != nil {
		t.Fatalf("mark expired job Worker loop inactive: %v", err)
	}

	s.runJanitorPass(ctx)
	var status, code string
	if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code FROM worker_jobs WHERE id = $1`, jobID).Scan(
		&status, &code,
	); err != nil {
		t.Fatalf("load expired Worker job: %v", err)
	}
	if status != "failed" || code != "worker_unavailable" {
		t.Fatalf("expired offline Worker job status = %q code = %q, want failed/worker_unavailable", status, code)
	}
}

func TestQueuedWorkerJobFailsWhenHeartbeatContinuesButJobLoopStalls(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}
	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, 'check-network-proxy', 'pending', '{}'::jsonb,
	    NOW() - INTERVAL '16 minutes', NOW() - INTERVAL '16 minutes')`,
		jobID, serverID); err != nil {
		t.Fatalf("insert queued Worker job: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_servers SET last_seen_at = NOW(),
	  last_job_activity_at = NOW() - INTERVAL '16 minutes' WHERE id = $1`, serverID); err != nil {
		t.Fatalf("mark queued job Worker loop inactive: %v", err)
	}

	s.runJanitorPass(ctx)
	var status, code string
	if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code
	  FROM worker_jobs WHERE id = $1`, jobID).Scan(&status, &code); err != nil {
		t.Fatalf("load queued Worker job: %v", err)
	}
	if status != "failed" || code != "worker_unavailable" {
		t.Fatalf("queued job status = %q code = %q, want failed/worker_unavailable", status, code)
	}
}

func TestActiveWorkerLeaseProtectsOlderQueuedJob(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}
	activeJobID := uuid.NewString()
	queuedJobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, lease_until, attempts, created_at, updated_at)
	  VALUES
	    ($1, $3, 'create-sandbox', 'leased', '{}'::jsonb,
	      NOW() + INTERVAL '30 minutes', 1, NOW() - INTERVAL '20 minutes', NOW()),
	    ($2, $3, 'check-network-proxy', 'pending', '{}'::jsonb,
	      NULL, 0, NOW() - INTERVAL '16 minutes', NOW() - INTERVAL '16 minutes')`,
		activeJobID, queuedJobID, serverID); err != nil {
		t.Fatalf("insert serial Worker jobs: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_servers SET last_seen_at = NOW(),
	  last_job_activity_at = NOW() - INTERVAL '16 minutes' WHERE id = $1`, serverID); err != nil {
		t.Fatalf("mark serial job Worker loop inactive: %v", err)
	}

	s.runJanitorPass(ctx)
	var activeStatus, queuedStatus string
	if err := s.pool.QueryRow(ctx, `SELECT active.status, queued.status
	  FROM worker_jobs active, worker_jobs queued
	  WHERE active.id = $1 AND queued.id = $2`, activeJobID, queuedJobID).Scan(
		&activeStatus, &queuedStatus,
	); err != nil {
		t.Fatalf("load serial Worker jobs: %v", err)
	}
	if activeStatus != "leased" || queuedStatus != "pending" {
		t.Fatalf("serial Worker job statuses = %q/%q, want leased/pending", activeStatus, queuedStatus)
	}
}

func TestClaimWithoutJobCommitsWorkerJobActivity(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker: %v", err)
	}
	var registeredActivityAt time.Time
	if err := s.pool.QueryRow(ctx, `SELECT last_job_activity_at
	  FROM managed_servers WHERE id = $1`, serverID).Scan(&registeredActivityAt); err != nil {
		t.Fatalf("load registered Worker job activity: %v", err)
	}
	if registeredActivityAt.Before(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("registered Worker job activity = %s, want a fresh timestamp", registeredActivityAt)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_servers
	  SET last_job_activity_at = NOW() - INTERVAL '20 minutes' WHERE id = $1`, serverID); err != nil {
		t.Fatalf("age Worker job activity: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Second)
	if _, err := s.ClaimWorkerJob(ctx, serverID, credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("claim empty Worker queue: err = %v, want ErrNoJob", err)
	}
	var activityAt time.Time
	if err := s.pool.QueryRow(ctx, `SELECT last_job_activity_at
	  FROM managed_servers WHERE id = $1`, serverID).Scan(&activityAt); err != nil {
		t.Fatalf("load Worker job activity: %v", err)
	}
	if activityAt.Before(startedAt) {
		t.Fatalf("Worker job activity = %s, want at or after %s", activityAt, startedAt)
	}
}
