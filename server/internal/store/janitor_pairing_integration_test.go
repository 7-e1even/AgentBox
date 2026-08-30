package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"testing"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"github.com/google/uuid"
)

// These tests exercise the janitor and server re-registration flows against a
// real PostgreSQL database. Set AGENTBOX_TEST_DATABASE_URL to a scratch
// database to run them.
func newIntegrationTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("AGENTBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTBOX_TEST_DATABASE_URL is not set; skipping integration test")
	}
	t.Setenv("AGENTBOX_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	ctx := t.Context()
	store, err := New(ctx, databaseURL, catalog.Catalog{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

func mustCreatePairingToken(t *testing.T, s *Store) string {
	t.Helper()
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO server_pairings
	  (id, token_hash, expires_at, created_at)
	  VALUES ($1, $2, NOW() + INTERVAL '10 minutes', NOW())`,
		uuid.NewString(), hashToken(token)); err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	return token
}

func testServerRegistration(serverID, pairingToken string) platform.ServerRegistration {
	return platform.ServerRegistration{
		PairingToken: pairingToken,
		ServerID:     serverID,
		Name:         "edge-01",
		Hostname:     "edge-01.internal",
		OS:           "linux",
		Arch:         "amd64",
	}
}

func TestRegisterServerReregistrationRequiresPreviousCredential(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()

	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("initial registration: %v", err)
	}

	// Re-registering a known serverID without the current credential is rejected.
	missingPrevious := testServerRegistration(serverID, mustCreatePairingToken(t, s))
	if _, _, err := s.RegisterServer(ctx, missingPrevious); !errors.Is(err, ErrWorkerUnauthorized) {
		t.Fatalf("re-registration without previous credential: err = %v, want ErrWorkerUnauthorized", err)
	}

	// A wrong previous credential is rejected the same way.
	wrongPrevious := testServerRegistration(serverID, mustCreatePairingToken(t, s))
	wrongPrevious.PreviousCredential = credential + "-tampered"
	if _, _, err := s.RegisterServer(ctx, wrongPrevious); !errors.Is(err, ErrWorkerUnauthorized) {
		t.Fatalf("re-registration with wrong previous credential: err = %v, want ErrWorkerUnauthorized", err)
	}

	// Presenting the current credential authorizes the rotation.
	rotation := testServerRegistration(serverID, mustCreatePairingToken(t, s))
	rotation.PreviousCredential = credential
	_, rotated, err := s.RegisterServer(ctx, rotation)
	if err != nil {
		t.Fatalf("re-registration with previous credential: %v", err)
	}
	if rotated == credential {
		t.Fatal("credential was not rotated")
	}

	// The rotated-out credential no longer authenticates; the new one does.
	if err := s.HeartbeatServer(ctx, serverID, credential, nil, nil, ""); !errors.Is(err, ErrWorkerUnauthorized) {
		t.Fatalf("heartbeat with rotated-out credential: err = %v, want ErrWorkerUnauthorized", err)
	}
	if err := s.HeartbeatServer(ctx, serverID, rotated, nil, nil, ""); err != nil {
		t.Fatalf("heartbeat with rotated credential: %v", err)
	}
}

func TestJanitorCleansExpiredRowsAndStuckSandboxes(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	if _, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s))); err != nil {
		t.Fatalf("register server: %v", err)
	}

	// Finished worker jobs: one older than the 7-day retention, one fresh.
	oldJobID, freshJobID := uuid.NewString(), uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, 'provision', 'succeeded', '{}'::jsonb, NOW() - INTERVAL '8 days', NOW() - INTERVAL '8 days')`,
		oldJobID, serverID); err != nil {
		t.Fatalf("insert old worker job: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, 'provision', 'succeeded', '{}'::jsonb, NOW(), NOW())`,
		freshJobID, serverID); err != nil {
		t.Fatalf("insert fresh worker job: %v", err)
	}

	// Automation runs: one older than the 30-day retention, one fresh.
	oldRunID, freshRunID := uuid.NewString(), uuid.NewString()
	for _, run := range []struct {
		id     string
		ageSQL string
	}{
		{oldRunID, "NOW() - INTERVAL '31 days'"},
		{freshRunID, "NOW()"},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO automation_runs
		  (id, project_id, automation_name, template_id, template_name, trigger_source, auth_mode,
		   payload_sha256, payload_bytes, status, received_at)
		  VALUES ($1, 'default', 'janitor-test', 'template', 'template', 'webhook', 'bearer',
		          $2, 64, 'succeeded', `+run.ageSQL+`)`,
			run.id, hashToken("payload")); err != nil {
			t.Fatalf("insert automation run %s: %v", run.id, err)
		}
	}

	// Login sessions: one expired, one valid.
	userID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO users
	  (id, name, username, email, password_hash, role, status, created_at, updated_at)
	  VALUES ($1, 'Janitor Test', $2, $3, $4, 'viewer', 'active', NOW(), NOW())`,
		userID, "janitor-"+uuid.NewString(), "janitor-"+uuid.NewString()+"@example.com", hashToken("password")); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	expiredSession := hashToken("expired-session")
	freshSession := hashToken("fresh-session")
	if _, err := s.pool.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id, expires_at, created_at)
	  VALUES ($1, $2, NOW() - INTERVAL '1 hour', NOW() - INTERVAL '2 hours')`,
		expiredSession, userID); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id, expires_at, created_at)
	  VALUES ($1, $2, NOW() + INTERVAL '1 day', NOW())`,
		freshSession, userID); err != nil {
		t.Fatalf("insert fresh session: %v", err)
	}

	// A sandbox stuck in 'requested' whose creation job was never claimed, and a
	// freshly requested sandbox that must be left alone.
	stuckSandboxID := "janitor-stuck-" + uuid.NewString()
	freshSandboxID := "janitor-fresh-" + uuid.NewString()
	for _, sandbox := range []struct {
		id     string
		ageSQL string
	}{
		{stuckSandboxID, "NOW() - INTERVAL '20 minutes'"},
		{freshSandboxID, "NOW()"},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
		  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
		  VALUES ($1, 'sandbox', 'default', $1, '', TRUE,
		          '{"status":"requested"}'::jsonb, `+sandbox.ageSQL+`, `+sandbox.ageSQL+`)`,
			sandbox.id); err != nil {
			t.Fatalf("insert sandbox %s: %v", sandbox.id, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, $3, 'provision', 'pending', '{}'::jsonb,
	          NOW() - INTERVAL '20 minutes', NOW() - INTERVAL '20 minutes')`,
		uuid.NewString(), serverID, stuckSandboxID); err != nil {
		t.Fatalf("insert stuck sandbox job: %v", err)
	}

	// Two passes must converge to the same state (idempotency).
	s.runJanitorPass(ctx)
	s.runJanitorPass(ctx)

	assertCount := func(query, label string, args []any, want int) {
		t.Helper()
		var count int
		if err := s.pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", label, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", label, count, want)
		}
	}
	assertCount(`SELECT COUNT(*) FROM worker_jobs WHERE id = $1`, "old worker job", []any{oldJobID}, 0)
	assertCount(`SELECT COUNT(*) FROM worker_jobs WHERE id = $1`, "fresh worker job", []any{freshJobID}, 1)
	assertCount(`SELECT COUNT(*) FROM automation_runs WHERE id = $1`, "old automation run", []any{oldRunID}, 0)
	assertCount(`SELECT COUNT(*) FROM automation_runs WHERE id = $1`, "fresh automation run", []any{freshRunID}, 1)
	assertCount(`SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1`, "expired session", []any{expiredSession}, 0)
	assertCount(`SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1`, "fresh session", []any{freshSession}, 1)

	var status, message string
	if err := s.pool.QueryRow(ctx, `SELECT spec->>'status', spec->>'message'
	  FROM control_resources WHERE id = $1`, stuckSandboxID).Scan(&status, &message); err != nil {
		t.Fatalf("load stuck sandbox: %v", err)
	}
	if status != "error" || message != "Worker 离线或任务超时未被认领" {
		t.Fatalf("stuck sandbox status = %q message = %q, want error state", status, message)
	}
	if err := s.pool.QueryRow(ctx, `SELECT spec->>'status'
	  FROM control_resources WHERE id = $1`, freshSandboxID).Scan(&status); err != nil {
		t.Fatalf("load fresh sandbox: %v", err)
	}
	if status != "requested" {
		t.Fatalf("fresh sandbox status = %q, want requested", status)
	}
}
