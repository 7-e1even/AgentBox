package store

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func newSystemLogIntegrationStore(t *testing.T) *Store {
	t.Helper()
	repository := newMigrationIntegrationStore(t)
	if err := repository.migrate(t.Context()); err != nil {
		t.Fatalf("migrate system log integration store: %v", err)
	}
	return repository
}

func TestSystemLogMaintenanceAppliesDeliveryClassRetention(t *testing.T) {
	repository := newSystemLogIntegrationStore(t)
	ctx := t.Context()
	prefix := "retention-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := repository.pool.Exec(ctx, `INSERT INTO system_logs
		(created_at, level, category, action, message, detail) VALUES
		($1, 'info', 'api', $6 || '-expired-best-effort', 'test', '{"delivery":"best-effort"}'::jsonb),
		($2, 'info', 'api', $6 || '-retained-best-effort', 'test', '{"delivery":"best-effort"}'::jsonb),
		($3, 'info', 'user', $6 || '-retained-transactional', 'test', '{"delivery":"transactional"}'::jsonb),
		($4, 'info', 'user', $6 || '-expired-transactional', 'test', '{"delivery":"transactional"}'::jsonb),
		($5, 'info', 'api', $6 || '-expired-default', 'test', '{}'::jsonb)`,
		now.Add(-31*24*time.Hour), now.Add(-29*24*time.Hour), now.Add(-31*24*time.Hour),
		now.Add(-366*24*time.Hour), now.Add(-31*24*time.Hour), prefix,
	); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := repository.pool.QueryRow(ctx, `SELECT COUNT(*) FROM system_logs
		WHERE action LIKE $1`, prefix+"%").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 5 {
		t.Fatalf("seeded logs before maintenance = %d, want 5", before)
	}

	if err := repository.runSystemLogMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := repository.pool.Query(ctx, `SELECT action FROM system_logs
		WHERE action LIKE $1 ORDER BY action`, prefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var retained []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		retained = append(retained, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{prefix + "-retained-best-effort", prefix + "-retained-transactional"}
	if len(retained) != len(want) || retained[0] != want[0] || retained[1] != want[1] {
		t.Fatalf("retained logs = %v, want %v", retained, want)
	}
}

func TestSystemLogMaintenanceCommitsNewestRowsPerDeliveryClass(t *testing.T) {
	repository := newSystemLogIntegrationStore(t)
	ctx := t.Context()
	prefix := "capacity-" + uuid.NewString()
	base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	for index := range 3 {
		for _, delivery := range []string{"best-effort", "transactional"} {
			if _, err := repository.pool.Exec(ctx, `INSERT INTO system_logs
				(created_at, level, category, action, message, detail)
				VALUES ($1, 'info', 'api', $2, 'test', jsonb_build_object('delivery', $3::text))`,
				base.Add(time.Duration(index)*time.Minute), prefix+"-"+delivery, delivery,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := repository.runSystemLogMaintenanceToLimits(ctx, 2, 2); err != nil {
		t.Fatal(err)
	}
	for _, delivery := range []string{"best-effort", "transactional"} {
		var count int
		var oldest time.Time
		if err := repository.pool.QueryRow(ctx, `SELECT COUNT(*), MIN(created_at)
			FROM system_logs WHERE action = $1`, prefix+"-"+delivery).Scan(&count, &oldest); err != nil {
			t.Fatal(err)
		}
		if count != 2 || !oldest.Equal(base.Add(time.Minute)) {
			t.Fatalf("delivery=%s count=%d oldest=%s", delivery, count, oldest)
		}
	}
}

func TestSystemLogMaintenanceElectionSkipsConcurrentReplica(t *testing.T) {
	repository := newSystemLogIntegrationStore(t)
	ctx := t.Context()
	action := "maintenance-try-lock-" + uuid.NewString()
	if _, err := repository.pool.Exec(ctx, `INSERT INTO system_logs
		(created_at, level, category, action, message, detail)
		VALUES (NOW() - INTERVAL '31 days', 'info', 'api', $1, 'test', '{}'::jsonb)`, action); err != nil {
		t.Fatal(err)
	}

	release := holdSystemLogMaintenanceLock(t, repository)
	if err := repository.runSystemLogMaintenance(ctx); err != nil {
		release()
		t.Fatalf("maintenance did not skip the elected replica: %v", err)
	}
	var count int
	if err := repository.pool.QueryRow(ctx, "SELECT COUNT(*) FROM system_logs WHERE action = $1", action).Scan(&count); err != nil {
		release()
		t.Fatal(err)
	}
	if count != 1 {
		release()
		t.Fatalf("skipped maintenance changed %d rows, want 1 retained", count)
	}
	release()

	if err := repository.runSystemLogMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.pool.QueryRow(ctx, "SELECT COUNT(*) FROM system_logs WHERE action = $1", action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("maintenance after lock release retained %d rows, want 0", count)
	}
}

func TestSystemLogMaintenanceWaitsForActiveWriter(t *testing.T) {
	repository := newSystemLogIntegrationStore(t)
	ctx := t.Context()
	action := "maintenance-writer-lock-" + uuid.NewString()
	if _, err := repository.pool.Exec(ctx, `INSERT INTO system_logs
		(created_at, level, category, action, message, detail)
		VALUES (NOW() - INTERVAL '31 days', 'info', 'api', $1, 'test', '{}'::jsonb)`, action); err != nil {
		t.Fatal(err)
	}

	release := holdSystemLogCapacityLock(t, repository)
	maintenanceCtx, cancelMaintenance := context.WithTimeout(ctx, 5*time.Second)
	defer cancelMaintenance()
	done := make(chan error, 1)
	go func() { done <- repository.runSystemLogMaintenance(maintenanceCtx) }()
	select {
	case err := <-done:
		release()
		t.Fatalf("maintenance bypassed the active writer lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("maintenance after writer unlock: %v", err)
		}
	case <-maintenanceCtx.Done():
		t.Fatalf("maintenance remained blocked after writer unlock: %v", context.Cause(maintenanceCtx))
	}
	var count int
	if err := repository.pool.QueryRow(ctx, "SELECT COUNT(*) FROM system_logs WHERE action = $1", action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("maintenance after writer unlock retained %d rows, want 0", count)
	}
}

func TestSystemLogProductionWritersShareCapacityLockAndEmptyDispatchSkipsIt(t *testing.T) {
	repository := newSystemLogIntegrationStore(t)
	ctx := t.Context()
	oldDeliveredID := uuid.NewString()
	oldLogAction := "empty-dispatch-old-log-" + uuid.NewString()
	if _, err := repository.pool.Exec(ctx, `INSERT INTO audit_outbox
		(id, created_at, entry, delivered_at)
		VALUES ($1, NOW() - INTERVAL '8 days', '{}'::jsonb, NOW() - INTERVAL '8 days')`, oldDeliveredID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.pool.Exec(ctx, `INSERT INTO system_logs
		(created_at, level, category, action, message, detail)
		VALUES (NOW() - INTERVAL '31 days', 'info', 'api', $1, 'test', '{}'::jsonb)`, oldLogAction); err != nil {
		t.Fatal(err)
	}

	release := holdSystemLogCapacityLock(t, repository)
	emptyCtx, cancelEmpty := context.WithTimeout(ctx, time.Second)
	if err := repository.DispatchAuditEvents(emptyCtx); err != nil {
		cancelEmpty()
		release()
		t.Fatalf("empty audit dispatch waited for the capacity lock: %v", err)
	}
	cancelEmpty()
	var oldEnvelopeCount, oldLogCount int
	if err := repository.pool.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM audit_outbox WHERE id = $1),
		(SELECT COUNT(*) FROM system_logs WHERE action = $2)`, oldDeliveredID, oldLogAction).Scan(
		&oldEnvelopeCount, &oldLogCount,
	); err != nil {
		release()
		t.Fatal(err)
	}
	if oldEnvelopeCount != 1 || oldLogCount != 1 {
		release()
		t.Fatalf("empty dispatch performed maintenance: envelope=%d log=%d", oldEnvelopeCount, oldLogCount)
	}

	insertCtx, cancelInsert := context.WithTimeout(ctx, 5*time.Second)
	defer cancelInsert()
	insertDone := make(chan error, 1)
	go func() {
		insertDone <- repository.InsertLogs(insertCtx, []platform.LogEntry{{
			CreatedAt: time.Now().UTC(), Level: platform.LogLevelInfo, Category: platform.LogCategoryAPI,
			Action: "capacity-lock-best-effort", Message: "test", Status: platform.LogStatusSuccess,
			Detail: map[string]any{"delivery": "best-effort"},
		}})
	}()
	select {
	case err := <-insertDone:
		release()
		t.Fatalf("InsertLogs bypassed capacity lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-insertDone:
		if err != nil {
			t.Fatalf("InsertLogs after capacity unlock: %v", err)
		}
	case <-insertCtx.Done():
		t.Fatalf("InsertLogs remained blocked after capacity unlock: %v", context.Cause(insertCtx))
	}

	auditID := uuid.NewString()
	auditEntry := platform.LogEntry{
		CreatedAt: time.Now().UTC(), Level: platform.LogLevelInfo, Category: platform.LogCategoryUser,
		Action: "capacity-lock-transactional", Message: "test", Status: platform.LogStatusSuccess,
		Detail: map[string]any{"delivery": "transactional"},
	}
	encoded, err := json.Marshal(auditEntry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.pool.Exec(ctx, `INSERT INTO audit_outbox (id, created_at, entry)
		VALUES ($1, $2, $3::jsonb)`, auditID, auditEntry.CreatedAt, encoded); err != nil {
		t.Fatal(err)
	}

	release = holdSystemLogCapacityLock(t, repository)
	dispatchCtx, cancelDispatch := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDispatch()
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- repository.DispatchAuditEvents(dispatchCtx) }()
	select {
	case err := <-dispatchDone:
		release()
		t.Fatalf("non-empty audit dispatch bypassed capacity lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-dispatchDone:
		if err != nil {
			t.Fatalf("audit dispatch after capacity unlock: %v", err)
		}
	case <-dispatchCtx.Done():
		t.Fatalf("audit dispatch remained blocked after capacity unlock: %v", context.Cause(dispatchCtx))
	}
	var delivered bool
	if err := repository.pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL
		FROM audit_outbox WHERE id = $1`, auditID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("audit event was not marked delivered")
	}
	if err := repository.pool.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM audit_outbox WHERE id = $1),
		(SELECT COUNT(*) FROM system_logs WHERE action = $2)`, oldDeliveredID, oldLogAction).Scan(
		&oldEnvelopeCount, &oldLogCount,
	); err != nil {
		t.Fatal(err)
	}
	if oldEnvelopeCount != 1 || oldLogCount != 1 {
		t.Fatalf("production writer performed maintenance: envelope=%d log=%d", oldEnvelopeCount, oldLogCount)
	}

	repository.runJanitorPass(ctx)
	if err := repository.pool.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM audit_outbox WHERE id = $1),
		(SELECT COUNT(*) FROM system_logs WHERE action = $2)`, oldDeliveredID, oldLogAction).Scan(
		&oldEnvelopeCount, &oldLogCount,
	); err != nil {
		t.Fatal(err)
	}
	if oldEnvelopeCount != 0 || oldLogCount != 0 {
		t.Fatalf("janitor did not perform deferred maintenance: envelope=%d log=%d", oldEnvelopeCount, oldLogCount)
	}
}

func holdSystemLogCapacityLock(t *testing.T, repository *Store) func() {
	return holdSystemLogLock(t, repository, systemLogCapacityLockKey)
}

func holdSystemLogMaintenanceLock(t *testing.T, repository *Store) func() {
	return holdSystemLogLock(t, repository, systemLogMaintenanceLockKey)
}

func holdSystemLogLock(t *testing.T, repository *Store, lockKey int64) func() {
	t.Helper()
	conn, err := repository.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&unlocked); err != nil || !unlocked {
			t.Errorf("release system log lock %d: unlocked=%v err=%v", lockKey, unlocked, err)
			_ = conn.Conn().Close(cleanupCtx)
		}
		conn.Release()
	})
	t.Cleanup(release)
	return release
}
