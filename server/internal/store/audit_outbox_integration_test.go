package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func auditTestUserInput() platform.UserInput {
	id := uuid.NewString()
	return platform.UserInput{Name: "Audit user", Username: "audit-" + id,
		Email: "audit-" + id + "@example.test", Password: "secret-password-not-for-audit",
		Role: platform.UserRoleViewer, Status: platform.UserStatusActive}
}

func TestAuditInsertFailureRollsBackUserAndPasswordSessionChanges(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	input := auditTestUserInput()
	user, err := s.CreateUser(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	sessionHash := hashToken("audit-session-secret")
	if _, err := s.AuthenticateUser(ctx, input.Username, input.Password, sessionHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION reject_audit_insert() RETURNS trigger LANGUAGE plpgsql AS $$
	  BEGIN RAISE EXCEPTION 'forced audit insert failure'; END $$;
	  CREATE TRIGGER reject_audit_insert BEFORE INSERT ON audit_outbox
	  FOR EACH ROW EXECUTE FUNCTION reject_audit_insert()`); err != nil {
		t.Fatal(err)
	}
	createdInput := auditTestUserInput()
	if _, err := s.CreateUser(ctx, createdInput); err == nil {
		t.Fatal("user creation succeeded despite failed audit insert")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username = $1`, createdInput.Username).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed creation left user count=%d error=%v", count, err)
	}
	input.Password = "new-secret-not-for-audit"
	if _, err := s.UpdateUser(ctx, user.ID, input); err == nil {
		t.Fatal("password update succeeded despite failed audit insert")
	}
	var storedHash []byte
	if err := s.pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword(storedHash, []byte("secret-password-not-for-audit")); err != nil {
		t.Fatalf("old password was not restored: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_sessions WHERE token_hash = $1`, sessionHash).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit failure revoked existing session count=%d error=%v", count, err)
	}
	if err := s.DeleteUser(ctx, user.ID); err == nil {
		t.Fatal("user deletion succeeded despite failed audit insert")
	}
	if err := s.DeleteSession(ctx, sessionHash); err == nil {
		t.Fatal("logout succeeded despite failed audit insert")
	}
	if _, err := s.AuthenticateUser(ctx, input.Username, "secret-password-not-for-audit",
		hashToken("failed-audit-login"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("login succeeded despite failed audit insert")
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_sessions WHERE user_id = $1`, user.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed audit mutated sessions count=%d error=%v", count, err)
	}
}

func TestSetupAdminAuditFailureRollsBackUserAndSession(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION reject_setup_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	  BEGIN RAISE EXCEPTION 'forced setup audit failure'; END $$;
	  CREATE TRIGGER reject_setup_audit BEFORE INSERT ON audit_outbox
	  FOR EACH ROW EXECUTE FUNCTION reject_setup_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetupAdmin(ctx, auditTestUserInput(), hashToken("setup-session-secret"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("administrator setup committed without its audit")
	}
	var users, sessions, events int
	if err := s.pool.QueryRow(ctx, `SELECT
	  (SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM user_sessions),
	  (SELECT COUNT(*) FROM audit_outbox)`).Scan(&users, &sessions, &events); err != nil {
		t.Fatal(err)
	}
	if users != 0 || sessions != 0 || events != 0 {
		t.Fatalf("failed setup left users=%d sessions=%d audits=%d", users, sessions, events)
	}
}

func TestBusinessRollbackDoesNotLeaveAuditEvent(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, name, created_at, updated_at) VALUES ('rolled-back-resource', 'skill', 'Temporary', NOW(), NOW())`); err != nil {
		t.Fatal(err)
	}
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryResource, Action: "create", ResourceID: "rolled-back-resource",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var resources, events int
	if err := s.pool.QueryRow(ctx, `SELECT
	  (SELECT COUNT(*) FROM control_resources WHERE id = 'rolled-back-resource'),
	  (SELECT COUNT(*) FROM audit_outbox)`).Scan(&resources, &events); err != nil {
		t.Fatal(err)
	}
	if resources != 0 || events != 0 {
		t.Fatalf("rollback left resources=%d events=%d", resources, events)
	}
}

func TestAuditDispatchRetainsFailureReplaysAfterReopenAndDeduplicates(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := platform.WithAuditActor(t.Context(), platform.AuditActor{Type: "user", ID: "audit-operator", Name: "Operator"})
	user, err := s.CreateUser(ctx, auditTestUserInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION reject_audit_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
	  BEGIN RAISE EXCEPTION 'forced audit delivery marker failure'; END $$;
	  CREATE TRIGGER reject_audit_delivery BEFORE UPDATE ON audit_outbox
	  FOR EACH ROW EXECUTE FUNCTION reject_audit_delivery()`); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchAuditEvents(ctx); err == nil {
		t.Fatal("dispatch succeeded despite failed delivered marker")
	}
	var logs, pending int
	if err := s.pool.QueryRow(ctx, `SELECT
	  (SELECT COUNT(*) FROM system_logs WHERE audit_event_id IS NOT NULL),
	  (SELECT COUNT(*) FROM audit_outbox WHERE delivered_at IS NULL)`).Scan(&logs, &pending); err != nil {
		t.Fatal(err)
	}
	if logs != 0 || pending != 1 {
		t.Fatalf("failed dispatch left logs=%d pending=%d, want 0/1", logs, pending)
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER reject_audit_delivery ON audit_outbox"); err != nil {
		t.Fatal(err)
	}
	// A fresh Store models process restart: the event is read from PostgreSQL,
	// without retaining an in-memory queue or prior dispatcher state.
	reopened, err := New(ctx, s.pool.Config().ConnString(), catalog.Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.DispatchAuditEvents(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate acknowledgement replay, then run two dispatchers concurrently.
	if _, err := s.pool.Exec(ctx, "UPDATE audit_outbox SET delivered_at = NULL"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		workers.Go(func() { errors <- reopened.DispatchAuditEvents(ctx) })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var actorID, delivery string
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*), MIN(actor_id), MIN(detail->>'delivery')
	  FROM system_logs WHERE resource_id = $1`, user.ID).Scan(&logs, &actorID, &delivery); err != nil {
		t.Fatal(err)
	}
	if logs != 1 || actorID != "audit-operator" || delivery != "transactional" {
		t.Fatalf("replayed log count=%d actor=%q delivery=%q", logs, actorID, delivery)
	}
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_outbox WHERE delivered_at IS NULL").Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending after replay=%d error=%v", pending, err)
	}
}

func TestAuditMetadataExcludesSecretsAndRequestBodies(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	const secret = "sensitive-value-never-in-audit"
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryCredential, Action: "update", Message: secret,
		Detail: map[string]any{"password": secret, "token": secret, "secret": secret,
			"spec": map[string]any{"nested": secret}, "error": secret, "body": secret,
			"passwordChanged": true, "serverId": "safe-server", "count": 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchAuditEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var persisted string
	if err := s.pool.QueryRow(ctx, `SELECT string_agg(entry::text, '') ||
	  (SELECT string_agg(message || detail::text, '') FROM system_logs) FROM audit_outbox`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, secret) || strings.Contains(persisted, `"spec"`) {
		t.Fatal("sensitive request content reached the audit outbox or system log")
	}
	if !strings.Contains(persisted, "passwordChanged") || !strings.Contains(persisted, "safe-server") {
		t.Fatal("safe audit metadata was lost")
	}
}

func TestAutomationAuditIsAtomicAndWebhookRetriesDoNotDuplicateIt(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	credentialID, templateID := "audit-credential", "audit-template"
	insertReferenceTestCredential(t, s, credentialID, `[{"id":"audit-model","name":"Audit model","group":"test","source":"manual"}]`)
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, spec, created_at, updated_at)
	  VALUES ($1, 'runtime', 'default', 'Audit template',
	    jsonb_build_object('driver', 'docker', 'credentialIds', jsonb_build_array($2::text)), NOW(), NOW())`,
		templateID, credentialID); err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	input := platform.AutomationInput{
		ProjectID: "default", Name: "Audit automation", Enabled: true, Secret: "original-audit-webhook-secret",
		Trigger:    platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
		TemplateID: templateID, ModelBindings: map[string]string{credentialID: "audit-model"},
	}
	automation, originalSecret, err := s.CreateAutomation(ctx, input, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, rotatedSecret, err := s.RotateAutomationSecret(ctx, automation.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	delivery := platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + rotatedSecret,
		IdempotencyKey: "audit-event-1", Body: []byte(`{"value":"private-webhook-body"}`),
	}
	first, err := s.TriggerAutomation(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.TriggerAutomation(ctx, delivery)
	if err != nil || !duplicate.Duplicate || duplicate.Run.ID != first.Run.ID {
		t.Fatalf("duplicate trigger=%#v err=%v", duplicate, err)
	}
	var triggerAudits int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_outbox
	  WHERE entry->>'action' = 'webhook' AND entry->'detail'->>'runId' = $1`, first.Run.ID).Scan(&triggerAudits); err != nil || triggerAudits != 1 {
		t.Fatalf("trigger audit count=%d err=%v", triggerAudits, err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION reject_automation_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	  BEGIN RAISE EXCEPTION 'forced automation audit failure'; END $$;
	  CREATE TRIGGER reject_automation_audit BEFORE INSERT ON audit_outbox
	  FOR EACH ROW EXECUTE FUNCTION reject_automation_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateAutomationSecret(ctx, automation.ID, userID); err == nil {
		t.Fatal("secret rotation committed without its audit")
	}
	_, storedSecret, err := s.GetAutomationSecret(ctx, automation.ID)
	if err != nil || storedSecret != rotatedSecret {
		t.Fatalf("failed rotation changed the stored secret: err=%v", err)
	}
	update := input
	update.Name = "Must not persist"
	if _, err := s.UpdateAutomation(ctx, automation.ID, update, userID); err == nil {
		t.Fatal("automation update committed without its audit")
	}
	if _, _, err := s.CreateAutomation(ctx, input, userID); err == nil {
		t.Fatal("automation creation committed without its audit")
	}
	if err := s.DeleteAutomation(ctx, automation.ID); err == nil {
		t.Fatal("automation deletion committed without its audit")
	}
	delivery.IdempotencyKey = "audit-event-2"
	if _, err := s.TriggerAutomation(ctx, delivery); err == nil {
		t.Fatal("automation run committed without its audit")
	}
	var automations, runs int
	var name, encodedEvents string
	if err := s.pool.QueryRow(ctx, `SELECT
	  (SELECT COUNT(*) FROM automations), (SELECT COUNT(*) FROM automation_runs),
	  (SELECT name FROM automations WHERE id = $1),
	  (SELECT string_agg(entry::text, '') FROM audit_outbox)`, automation.ID).Scan(&automations, &runs, &name, &encodedEvents); err != nil {
		t.Fatal(err)
	}
	if automations != 1 || runs != 1 || name != input.Name {
		t.Fatalf("failed audit leaked business changes automations=%d runs=%d name=%q", automations, runs, name)
	}
	for _, sensitive := range []string{originalSecret, rotatedSecret, "private-webhook-body"} {
		if strings.Contains(encodedEvents, sensitive) {
			t.Fatal("automation audit contains a secret or webhook payload")
		}
	}
}
