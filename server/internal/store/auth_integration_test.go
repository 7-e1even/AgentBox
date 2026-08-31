package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestUpdateUserRollsBackPasswordWhenSessionInvalidationFails(t *testing.T) {
	store := newIntegrationTestStore(t)
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	input := platform.UserInput{
		Name: "Transaction Test", Username: "transaction-" + suffix,
		Email: "transaction-" + suffix + "@example.com", Password: "old-password-123",
		Role: platform.UserRoleViewer, Status: platform.UserStatusActive,
	}
	user, err := store.CreateUser(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	sessionHash := hashToken("session-" + suffix)
	if _, err := store.pool.Exec(ctx, `INSERT INTO user_sessions
		(token_hash, user_id, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
		sessionHash, user.ID, time.Now().UTC().Add(time.Hour), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	functionName := "fail_session_delete_" + suffix
	triggerName := "fail_session_delete_" + suffix
	quotedFunction := pgIdentifier(functionName)
	quotedTrigger := pgIdentifier(triggerName)
	if _, err := store.pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.user_id = '%s'::uuid THEN RAISE EXCEPTION 'forced session delete failure'; END IF;
			RETURN OLD;
		END $body$;
		CREATE TRIGGER %s BEFORE DELETE ON user_sessions FOR EACH ROW EXECUTE FUNCTION %s()`,
		quotedFunction, user.ID, quotedTrigger, quotedFunction)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.pool.Exec(cleanupCtx, "DROP TRIGGER IF EXISTS "+quotedTrigger+" ON user_sessions")
		_, _ = store.pool.Exec(cleanupCtx, "DROP FUNCTION IF EXISTS "+quotedFunction+"()")
		_, _ = store.pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	input.Password = "new-password-456"
	if _, err := store.UpdateUser(ctx, user.ID, input); err == nil {
		t.Fatal("user update succeeded despite forced session invalidation failure")
	}
	var passwordHash []byte
	if err := store.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword(passwordHash, []byte("old-password-123")); err != nil {
		t.Fatalf("old password was not restored by rollback: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(passwordHash, []byte("new-password-456")); err == nil {
		t.Fatal("new password persisted despite rollback")
	}
	var sessionCount int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1`, sessionHash).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Fatalf("session count = %d, want 1 after rollback", sessionCount)
	}
}

func TestAuthenticateUserLoginLockWinsPasswordChange(t *testing.T) {
	store := newIntegrationTestStore(t)
	ctx := t.Context()
	user, input := createAuthRaceTestUser(t, store, "login-first")
	loginSessionHash := hashToken("login-first-session-" + uuid.NewString())
	releaseSessionInsert := installUserSessionMutationGate(t, store, user.ID, "INSERT")
	username, oldPassword := input.Username, input.Password

	loginResult := make(chan error, 1)
	go func() {
		_, err := store.AuthenticateUser(ctx, username, oldPassword, loginSessionHash, time.Now().UTC().Add(time.Hour))
		loginResult <- err
	}()
	waitForBlockedAuthQuery(t, store, "INSERT INTO user_sessions")

	input.Password = "new-password-456"
	updateResult := make(chan error, 1)
	go func() {
		_, err := store.UpdateUser(ctx, user.ID, input)
		updateResult <- err
	}()
	waitForBlockedAuthQuery(t, store, "UPDATE users SET name")

	releaseSessionInsert()
	if err := awaitAuthMutation(t, loginResult, "login"); err != nil {
		t.Fatalf("login while holding the user lock: %v", err)
	}
	if err := awaitAuthMutation(t, updateResult, "password update"); err != nil {
		t.Fatalf("password update after login: %v", err)
	}

	var sessionCount int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1`, loginSessionHash).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("login session count = %d, want 0 after the later password update", sessionCount)
	}
}

func TestAuthenticateUserRejectsOldPasswordWhenPasswordChangeCommitsFirst(t *testing.T) {
	store := newIntegrationTestStore(t)
	ctx := t.Context()
	user, input := createAuthRaceTestUser(t, store, "password-first")
	seedSessionHash := hashToken("password-first-seed-session-" + uuid.NewString())
	if _, err := store.pool.Exec(ctx, `INSERT INTO user_sessions
		(token_hash, user_id, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
		seedSessionHash, user.ID, time.Now().UTC().Add(time.Hour), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	releaseSessionDelete := installUserSessionMutationGate(t, store, user.ID, "DELETE")

	oldPassword := input.Password
	input.Password = "new-password-456"
	updateResult := make(chan error, 1)
	go func() {
		_, err := store.UpdateUser(ctx, user.ID, input)
		updateResult <- err
	}()
	waitForBlockedAuthQuery(t, store, "DELETE FROM user_sessions WHERE user_id")

	loginSessionHash := hashToken("password-first-login-session-" + uuid.NewString())
	loginResult := make(chan error, 1)
	go func() {
		_, err := store.AuthenticateUser(ctx, input.Username, oldPassword, loginSessionHash, time.Now().UTC().Add(time.Hour))
		loginResult <- err
	}()
	select {
	case err := <-loginResult:
		t.Fatalf("old-password login completed before the password update committed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	releaseSessionDelete()
	if err := awaitAuthMutation(t, updateResult, "password update"); err != nil {
		t.Fatalf("password update: %v", err)
	}
	if err := awaitAuthMutation(t, loginResult, "old-password login"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old-password login error = %v, want ErrUnauthorized", err)
	}

	var sessionCount int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1 OR token_hash = $2`,
		seedSessionHash, loginSessionHash).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("old and attempted login session count = %d, want 0", sessionCount)
	}
}

func createAuthRaceTestUser(t *testing.T, store *Store, label string) (platform.User, platform.UserInput) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	input := platform.UserInput{
		Name: label + " auth race", Username: label + "-" + suffix,
		Email: label + "-" + suffix + "@example.com", Password: "old-password-123",
		Role: platform.UserRoleViewer, Status: platform.UserStatusActive,
	}
	user, err := store.CreateUser(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	return user, input
}

func installUserSessionMutationGate(t *testing.T, store *Store, userID, event string) func() {
	t.Helper()
	if event != "INSERT" && event != "DELETE" {
		t.Fatalf("unsupported session mutation event %q", event)
	}
	ctx := t.Context()
	lockKey := int64(uuid.New().ID())
	gate, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire session mutation gate: %v", err)
	}
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		gate.Release()
		t.Fatalf("lock session mutation gate: %v", err)
	}

	functionName := "gate_session_mutation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	triggerName := functionName
	quotedFunction := pgIdentifier(functionName)
	quotedTrigger := pgIdentifier(triggerName)
	if _, err := store.pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF TG_OP = 'DELETE' THEN
				IF OLD.user_id = '%s'::uuid THEN PERFORM pg_advisory_xact_lock(%d); END IF;
				RETURN OLD;
			END IF;
			IF NEW.user_id = '%s'::uuid THEN PERFORM pg_advisory_xact_lock(%d); END IF;
			RETURN NEW;
		END $body$;
		CREATE TRIGGER %s BEFORE %s ON user_sessions FOR EACH ROW EXECUTE FUNCTION %s()`,
		quotedFunction, userID, lockKey, userID, lockKey, quotedTrigger, event, quotedFunction)); err != nil {
		_, _ = gate.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey)
		gate.Release()
		t.Fatalf("create session mutation gate: %v", err)
	}

	released := false
	release := func() {
		if released {
			return
		}
		released = true
		unlockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var unlocked bool
		if err := gate.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&unlocked); err != nil {
			t.Errorf("unlock session mutation gate: %v", err)
		} else if !unlocked {
			t.Error("session mutation gate was not held")
		}
		gate.Release()
	}
	t.Cleanup(func() {
		release()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.pool.Exec(cleanupCtx, "DROP TRIGGER IF EXISTS "+quotedTrigger+" ON user_sessions")
		_, _ = store.pool.Exec(cleanupCtx, "DROP FUNCTION IF EXISTS "+quotedFunction+"()")
	})
	return release
}

func waitForBlockedAuthQuery(t *testing.T, store *Store, queryFragment string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err := store.pool.QueryRow(t.Context(), `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND state = 'active'
			  AND wait_event_type = 'Lock'
			  AND STRPOS(query, $1) > 0
		)`, queryFragment).Scan(&blocked); err != nil {
			t.Fatalf("inspect blocked auth query: %v", err)
		}
		if blocked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for blocked query containing %q", queryFragment)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func awaitAuthMutation(t *testing.T, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func pgIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
