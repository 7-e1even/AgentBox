package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

type cancellationFixture struct {
	store      *Store
	serverID   string
	credential string
	sandboxID  string
	jobID      string
}

func newCancellationFixture(t *testing.T) cancellationFixture {
	t.Helper()
	s := newIntegrationTestStore(t)
	fixture := cancellationFixture{store: s, serverID: uuid.NewString(), sandboxID: "cancel-" + uuid.NewString(), jobID: uuid.NewString()}
	_, credential, err := s.RegisterServer(t.Context(), testServerRegistration(fixture.serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatal(err)
	}
	fixture.credential = credential
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO control_resources
      (id, kind, project_id, name, description, enabled, spec, created_at, updated_at, observed_generation)
      VALUES ($1, 'sandbox', 'default', 'Cancellation test', '', TRUE,
        jsonb_build_object(
          'serverId', $2::text, 'driver', 'docker', 'status', 'requested',
          'credentialedProxyIdAtCreation', ''::text
        ), NOW(), NOW(), 0)`,
		fixture.sandboxID, fixture.serverID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO worker_jobs
      (id, server_id, resource_id, action, status, payload, created_at, updated_at, resource_generation)
      VALUES ($1, $2, $3, 'create-sandbox', 'pending',
        jsonb_build_object('sandboxId', $3::text, 'driver', 'docker'), NOW(), NOW(), 1)`,
		fixture.jobID, fixture.serverID, fixture.sandboxID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f cancellationFixture) claim(t *testing.T, support bool) {
	t.Helper()
	job, err := f.store.ClaimWorkerJob(t.Context(), f.serverID, f.credential)
	if err != nil || job.ID != f.jobID || job.LeaseGeneration != 1 {
		t.Fatalf("claim = %+v, %v", job, err)
	}
	if support {
		if _, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func (f cancellationFixture) cancel(t *testing.T) platform.Resource {
	t.Helper()
	resource, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "cancel-install")
	if err != nil {
		t.Fatal(err)
	}
	return resource
}

func (f cancellationFixture) ack(t *testing.T, code string) {
	t.Helper()
	if err := f.store.CompleteWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobResult{
		LeaseGeneration: 1, Message: sandboxCancelledMessage,
		Error: &platform.WorkerJobError{Code: code, Stage: "cancel"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCancelPendingInstallationNeverRunsAndCanBeDeleted(t *testing.T) {
	f := newCancellationFixture(t)
	resource := f.cancel(t)
	if resource.Spec["status"] != "cancelled" {
		t.Fatalf("pending cancellation = %+v", resource.Spec)
	}
	f.cancel(t) // Idempotent when the first cancellation already finished.
	if _, err := f.store.ClaimWorkerJob(t.Context(), f.serverID, f.credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("cancelled pending job claimed: %v", err)
	}
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "start"); !platform.IsValidationError(err) {
		t.Fatalf("cancelled installation can start: %v", err)
	}
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "delete"); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimWorkerJob(t.Context(), f.serverID, f.credential)
	if err != nil || job.Action != "delete-sandbox" {
		t.Fatalf("delete claim = %+v, %v", job, err)
	}
	if err := f.store.CompleteWorkerJob(t.Context(), f.serverID, f.credential, job.ID, platform.WorkerJobResult{LeaseGeneration: 1, Success: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetResource(t.Context(), f.sandboxID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("cancelled sandbox not deleted: %v", err)
	}
}

func TestCancellationRequiresSupportingAuthenticatedLease(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, false)
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "cancel-install"); !platform.IsValidationError(err) || !strings.Contains(err.Error(), "更新 Worker") {
		t.Fatalf("legacy Worker cancellation error = %v", err)
	}
	for _, generation := range []int{0, 2} {
		if _, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: generation}); !errors.Is(err, ErrResourceNotFound) {
			t.Fatalf("generation %d accepted: %v", generation, err)
		}
	}
	if _, err := f.store.ControlWorkerJob(t.Context(), f.serverID, strings.Repeat("x", 40), f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1}); !errors.Is(err, ErrWorkerUnauthorized) {
		t.Fatalf("wrong credential accepted: %v", err)
	}
	otherID := uuid.NewString()
	_, otherCredential, err := f.store.RegisterServer(t.Context(), testServerRegistration(otherID, mustCreatePairingToken(t, f.store)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ControlWorkerJob(t.Context(), otherID, otherCredential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("different Worker accessed job: %v", err)
	}
	control, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1})
	if err != nil || control.CancelRequested {
		t.Fatalf("initial control = %+v, %v", control, err)
	}
	resource := f.cancel(t)
	if resource.Spec["status"] != "cancelling" {
		t.Fatalf("leased cancellation = %+v", resource.Spec)
	}
}

func TestCancellingInstallationFencesProgressAndCompletionUntilCleanup(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	f.cancel(t)
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "delete"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete allowed before cleanup acknowledgement: %v", err)
	}
	progress, err := f.store.ReportWorkerJobProgress(t.Context(), f.serverID, f.credential, f.jobID,
		platform.WorkerJobProgressInput{LeaseGeneration: 1, Stage: "runtime", Message: "late progress"})
	if err != nil || progress.Status != "cancelling" || progress.Message != sandboxCancellationMessage || progress.FinishedAt != nil {
		t.Fatalf("late progress overwrote cancellation: %+v, %v", progress, err)
	}
	for _, result := range []platform.WorkerJobResult{
		{LeaseGeneration: 1, Success: true},
		{LeaseGeneration: 1, Error: &platform.WorkerJobError{Code: "worker_failed"}},
		{LeaseGeneration: 2, Error: &platform.WorkerJobError{Code: "job_cancelled"}},
	} {
		if err := f.store.CompleteWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, result); !errors.Is(err, ErrResourceNotFound) {
			t.Fatalf("invalid cancellation completion accepted: %+v, %v", result, err)
		}
	}
	control, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1})
	if err != nil || !control.CancelRequested {
		t.Fatalf("cancel control = %+v, %v", control, err)
	}
	f.ack(t, "job_cancelled")
	resource, err := f.store.GetResource(t.Context(), f.sandboxID)
	if err != nil || resource.Spec["status"] != "cancelled" || resource.ObservedGeneration != 0 {
		t.Fatalf("cleanup acknowledgement = %+v, %v", resource, err)
	}
	var status, code string
	var retryable bool
	if err := f.store.pool.QueryRow(t.Context(), `SELECT status, result_error_code, result_error_retryable FROM worker_jobs WHERE id=$1`, f.jobID).Scan(&status, &code, &retryable); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "job_cancelled" || retryable {
		t.Fatalf("cancelled job = %s/%s retryable=%v", status, code, retryable)
	}
}

func TestCancellationCleanupFailureDoesNotClaimCancelledOrAllowStart(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	f.cancel(t)
	f.ack(t, "cancellation_cleanup_failed")
	resource, err := f.store.GetResource(t.Context(), f.sandboxID)
	if err != nil || resource.Spec["status"] != "error" {
		t.Fatalf("cleanup failure = %+v, %v", resource, err)
	}
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "start"); !platform.IsValidationError(err) {
		t.Fatalf("incomplete cancelled sandbox can start: %v", err)
	}
}

func TestCancelledLeaseWaitsForOriginalWorkerAfterExpiry(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	f.cancel(t)
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE worker_jobs SET lease_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, f.jobID); err != nil {
		t.Fatal(err)
	}
	f.store.runJanitorPass(t.Context())
	if _, err := f.store.ClaimWorkerJob(t.Context(), f.serverID, f.credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("cancelled lease replayed: %v", err)
	}
	control, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1})
	if err != nil || !control.CancelRequested {
		t.Fatalf("original Worker cannot resume cleanup: %+v, %v", control, err)
	}
	var leaseUntil time.Time
	if err := f.store.pool.QueryRow(t.Context(), `SELECT lease_until FROM worker_jobs WHERE id=$1`, f.jobID).Scan(&leaseUntil); err != nil || !leaseUntil.After(time.Now()) {
		t.Fatalf("cancel lease was not renewed: %v, %v", leaseUntil, err)
	}
	f.ack(t, "job_cancelled")
}

func TestInstallationCompletionWinningRaceCannotBeCancelled(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	if err := f.store.CompleteWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobResult{LeaseGeneration: 1, Success: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "cancel-install"); !platform.IsValidationError(err) {
		t.Fatalf("completed installation cancellation accepted: %v", err)
	}
	resource, err := f.store.GetResource(t.Context(), f.sandboxID)
	if err != nil || resource.Spec["status"] != "running" {
		t.Fatalf("completed sandbox was changed: %+v, %v", resource, err)
	}
}

func TestInstallationCancellationAndCompletionRaceHasOneWinner(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	start := make(chan struct{})
	cancelResult := make(chan error, 1)
	completeResult := make(chan error, 1)
	go func() {
		<-start
		_, err := f.store.OperateSandbox(t.Context(), f.sandboxID, "cancel-install")
		cancelResult <- err
	}()
	go func() {
		<-start
		completeResult <- f.store.CompleteWorkerJob(t.Context(), f.serverID, f.credential, f.jobID,
			platform.WorkerJobResult{LeaseGeneration: 1, Success: true})
	}()
	close(start)
	cancelErr, completeErr := <-cancelResult, <-completeResult
	resource, err := f.store.GetResource(t.Context(), f.sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelErr == nil {
		if !errors.Is(completeErr, ErrResourceNotFound) || resource.Spec["status"] != "cancelling" {
			t.Fatalf("cancellation won but completion overwrote it: complete=%v spec=%+v", completeErr, resource.Spec)
		}
		f.ack(t, "job_cancelled")
	} else if !platform.IsValidationError(cancelErr) || completeErr != nil || resource.Spec["status"] != "running" {
		t.Fatalf("completion race was not atomic: cancel=%v complete=%v spec=%+v", cancelErr, completeErr, resource.Spec)
	}
}

func TestWorkerControlCannotReviveUncancelledExpiredOrChangedResourceLease(t *testing.T) {
	f := newCancellationFixture(t)
	f.claim(t, true)
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE worker_jobs SET lease_until = NOW() - INTERVAL '1 minute' WHERE id=$1`, f.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("expired normal lease was revived: %v", err)
	}
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE worker_jobs SET lease_until = NOW() + INTERVAL '1 minute' WHERE id=$1`, f.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE control_resources SET generation = generation + 1 WHERE id=$1`, f.sandboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ControlWorkerJob(t.Context(), f.serverID, f.credential, f.jobID, platform.WorkerJobControlInput{LeaseGeneration: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed resource lease was accepted: %v", err)
	}
}
