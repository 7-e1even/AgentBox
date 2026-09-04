package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"agentbox/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrAutomationDisabled            = errors.New("automation disabled")
	ErrWebhookUnauthorized           = errors.New("webhook unauthorized")
	ErrAutomationRateLimit           = errors.New("automation rate limit")
	ErrAutomationIdempotencyConflict = errors.New("automation idempotency conflict")
)

const (
	automationRequestsPerMinute = 30
	automationInflightLimit     = 5
	webhookSignatureWindow      = 5 * time.Minute
)

const automationColumns = `id::text, project_id, name, description, enabled,
  trigger_type, auth_mode, endpoint_id::text, secret_last_four,
  COALESCE(template_id, ''), model_bindings, created_by::text, updated_by::text,
  last_triggered_at, secret_rotated_at, created_at, updated_at`

const automationRunColumns = `id::text, automation_id::text, project_id, automation_name,
  COALESCE(endpoint_id::text, ''), template_id, template_name,
  trigger_source, auth_mode, event_id, event_type, event_source, event_time,
  idempotency_fingerprint,
  encode(payload_sha256, 'hex'), payload_bytes, COALESCE(encode(input_sha256, 'hex'), ''),
  status, sandbox_id, worker_job_id::text, error_code, error_message,
  error_stage, error_retryable, error_details,
  received_at, queued_at, started_at, finished_at, provisioning`

type storedAutomation struct {
	Automation       platform.Automation
	SecretHash       []byte
	SecretCiphertext []byte
	SecretNonce      []byte
}

func (s *Store) ListAutomations(ctx context.Context, projectID string) ([]platform.Automation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+automationColumns+` FROM automations
	    WHERE project_id = $1 AND action_type = 'create-sandbox'
	    ORDER BY enabled DESC, updated_at DESC`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("list automations: %w", err)
	}
	defer rows.Close()
	result := make([]platform.Automation, 0)
	for rows.Next() {
		automation, err := scanAutomation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan automation: %w", err)
		}
		result = append(result, automation)
	}
	return result, rows.Err()
}

func (s *Store) GetAutomation(ctx context.Context, id string) (platform.Automation, error) {
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `SELECT `+automationColumns+`
    FROM automations WHERE id = $1 AND action_type = 'create-sandbox'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, fmt.Errorf("get automation: %w", err)
	}
	return automation, nil
}

func (s *Store) GetAutomationSecret(ctx context.Context, id string) (platform.Automation, string, error) {
	stored, err := scanStoredAutomation(s.pool.QueryRow(ctx, `SELECT `+automationColumns+`,
		secret_hash, secret_ciphertext, secret_nonce FROM automations
		WHERE id = $1 AND action_type = 'create-sandbox'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, "", ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, "", fmt.Errorf("get automation secret: %w", err)
	}
	secret, err := decryptSecret(s.secretKey, stored.SecretCiphertext, stored.SecretNonce)
	if err != nil {
		return platform.Automation{}, "", err
	}
	return stored.Automation, secret, nil
}

func (s *Store) CreateAutomation(ctx context.Context, input platform.AutomationInput, userID string) (platform.Automation, string, error) {
	if platform.AuditActorFromContext(ctx).Type == "" {
		ctx = platform.WithAuditActor(ctx, platform.AuditActor{Type: "user", ID: userID})
	}
	platform.NormalizeAutomationInput(&input)
	if err := platform.ValidateAutomationInput(input); err != nil {
		return platform.Automation{}, "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Automation{}, "", fmt.Errorf("begin create automation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneMutation(ctx, tx); err != nil {
		return platform.Automation{}, "", err
	}
	templateResource, err := loadAutomationTemplate(ctx, tx, input.ProjectID, input.TemplateID)
	if err != nil {
		return platform.Automation{}, "", err
	}
	if err := s.validateNetworkProxyBinding(ctx, tx, templateResource.Spec, "自动化模板"); err != nil {
		return platform.Automation{}, "", err
	}
	if err := validateAutomationModelBindings(ctx, tx, templateResource.Spec, input.ModelBindings); err != nil {
		return platform.Automation{}, "", err
	}
	secret := input.Secret
	if secret == "" {
		var err error
		secret, err = newWebhookSecret()
		if err != nil {
			return platform.Automation{}, "", err
		}
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, secret)
	if err != nil {
		return platform.Automation{}, "", err
	}
	now := time.Now().UTC()
	automation, err := scanAutomation(tx.QueryRow(ctx, `INSERT INTO automations
    (id, project_id, name, description, enabled, trigger_type, action_type, auth_mode,
     endpoint_id, secret_hash, secret_ciphertext, secret_nonce, secret_last_four,
     template_id, model_bindings, input_template,
     created_by, updated_by, secret_rotated_at, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, 'webhook', 'create-sandbox', $6, $7, $8, $9, $10,
	  $11, $12, $13, '{}'::text, $14, $14, $15, $15, $15)
    RETURNING `+automationColumns,
		uuid.NewString(), input.ProjectID, input.Name, input.Description, input.Enabled,
		input.Trigger.AuthMode, uuid.NewString(), hashToken(secret), ciphertext, nonce,
		secretStorageMarker(secret), input.TemplateID, automationModelBindingsJSON(input.ModelBindings), userID, now,
	))
	if err != nil {
		return platform.Automation{}, "", mapResourceError(err)
	}
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "create", ActorID: userID,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
		Detail: map[string]any{"projectId": automation.ProjectID, "templateId": automation.TemplateID},
	}); err != nil {
		return platform.Automation{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.Automation{}, "", mapResourceError(err)
	}
	return automation, secret, nil
}

func (s *Store) UpdateAutomation(ctx context.Context, id string, input platform.AutomationInput, userID string) (platform.Automation, error) {
	if platform.AuditActorFromContext(ctx).Type == "" {
		ctx = platform.WithAuditActor(ctx, platform.AuditActor{Type: "user", ID: userID})
	}
	platform.NormalizeAutomationInput(&input)
	if err := platform.ValidateAutomationInput(input); err != nil {
		return platform.Automation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Automation{}, fmt.Errorf("begin update automation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneMutation(ctx, tx); err != nil {
		return platform.Automation{}, err
	}
	templateResource, err := loadAutomationTemplate(ctx, tx, input.ProjectID, input.TemplateID)
	if err != nil {
		return platform.Automation{}, err
	}
	if err := s.validateNetworkProxyBinding(ctx, tx, templateResource.Spec, "自动化模板"); err != nil {
		return platform.Automation{}, err
	}
	if err := validateAutomationModelBindings(ctx, tx, templateResource.Spec, input.ModelBindings); err != nil {
		return platform.Automation{}, err
	}
	automation, err := scanAutomation(tx.QueryRow(ctx, `UPDATE automations SET
	project_id = $1, name = $2, description = $3, enabled = $4,
	action_type = 'create-sandbox', auth_mode = $5, template_id = $6,
	model_bindings = $7, updated_by = $8, updated_at = $9
	WHERE id = $10 AND action_type = 'create-sandbox' RETURNING `+automationColumns,
		input.ProjectID, input.Name, input.Description, input.Enabled,
		input.Trigger.AuthMode, input.TemplateID, automationModelBindingsJSON(input.ModelBindings),
		userID, time.Now().UTC(), id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, mapResourceError(err)
	}
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "update", ActorID: userID,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
		Detail: map[string]any{"projectId": automation.ProjectID, "templateId": automation.TemplateID, "enabled": automation.Enabled},
	}); err != nil {
		return platform.Automation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.Automation{}, mapResourceError(err)
	}
	return automation, nil
}

func (s *Store) DeleteAutomation(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete automation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneMutation(ctx, tx); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, "DELETE FROM automations WHERE id = $1 AND action_type = 'create-sandbox'", id)
	if err != nil {
		return fmt.Errorf("delete automation: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrResourceNotFound
	}
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "delete", ResourceKind: "automation", ResourceID: id,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete automation: %w", err)
	}
	return nil
}

func (s *Store) RotateAutomationSecret(ctx context.Context, id, userID string) (platform.Automation, string, error) {
	if platform.AuditActorFromContext(ctx).Type == "" {
		ctx = platform.WithAuditActor(ctx, platform.AuditActor{Type: "user", ID: userID})
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return platform.Automation{}, "", err
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, secret)
	if err != nil {
		return platform.Automation{}, "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Automation{}, "", fmt.Errorf("begin rotate automation secret: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneMutation(ctx, tx); err != nil {
		return platform.Automation{}, "", err
	}
	now := time.Now().UTC()
	automation, err := scanAutomation(tx.QueryRow(ctx, `UPDATE automations SET
    secret_hash = $1, secret_ciphertext = $2, secret_nonce = $3, secret_last_four = $4,
    updated_by = $5, secret_rotated_at = $6, updated_at = $6
	    WHERE id = $7 AND action_type = 'create-sandbox' RETURNING `+automationColumns,
		hashToken(secret), ciphertext, nonce, secretStorageMarker(secret), userID, now, id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, "", ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, "", fmt.Errorf("rotate automation secret: %w", err)
	}
	if err := s.appendAuditEvent(ctx, tx, platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: "rotate-secret", ActorID: userID,
		ResourceKind: "automation", ResourceID: automation.ID, ResourceName: automation.Name,
	}); err != nil {
		return platform.Automation{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.Automation{}, "", fmt.Errorf("commit rotate automation secret: %w", err)
	}
	return automation, secret, nil
}

func (s *Store) TriggerAutomation(ctx context.Context, delivery platform.AutomationDelivery) (platform.AutomationTriggerResult, error) {
	return s.triggerAutomation(ctx, delivery.EndpointID, true, "webhook", delivery)
}

func (s *Store) TestAutomation(ctx context.Context, id string, body []byte) (platform.AutomationTriggerResult, error) {
	return s.triggerAutomation(ctx, id, false, "manual-test", platform.AutomationDelivery{Body: body})
}

func (s *Store) triggerAutomation(
	ctx context.Context,
	identifier string,
	byEndpoint bool,
	triggerSource string,
	delivery platform.AutomationDelivery,
) (platform.AutomationTriggerResult, error) {
	if err := validateAutomationIdempotencyKey(delivery.IdempotencyKey); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if byEndpoint {
		stored, err := scanStoredAutomation(s.pool.QueryRow(ctx, `SELECT `+automationColumns+`,
			secret_hash, secret_ciphertext, secret_nonce FROM automations
			WHERE endpoint_id = $1 AND action_type = 'create-sandbox'`, identifier))
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.AutomationTriggerResult{}, ErrResourceNotFound
		}
		if err != nil {
			return platform.AutomationTriggerResult{}, fmt.Errorf("pre-authenticate automation trigger: %w", err)
		}
		if err := s.verifyAutomationDelivery(stored, delivery); err != nil {
			return platform.AutomationTriggerResult{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("begin automation trigger: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockControlPlaneReferences(ctx, tx); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	where := "id = $1"
	if byEndpoint {
		where = "endpoint_id = $1"
	}
	stored, err := scanStoredAutomation(tx.QueryRow(ctx, `SELECT `+automationColumns+`,
		secret_hash, secret_ciphertext, secret_nonce FROM automations
		WHERE `+where+` AND action_type = 'create-sandbox' FOR UPDATE`, identifier))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.AutomationTriggerResult{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("load automation trigger: %w", err)
	}
	if byEndpoint {
		if err := s.verifyAutomationDelivery(stored, delivery); err != nil {
			return platform.AutomationTriggerResult{}, err
		}
		ctx = platform.WithAuditActor(ctx, platform.AuditActor{
			Type: "webhook", ID: stored.Automation.ID, Name: stored.Automation.Name,
		})
	}
	if !stored.Automation.Enabled {
		return platform.AutomationTriggerResult{}, ErrAutomationDisabled
	}

	receivedAt := time.Now().UTC()
	event := canonicalAutomationEvent(delivery, stored.Automation.Trigger.AuthMode, receivedAt)
	payloadHash := sha256.Sum256(delivery.Body)
	idempotencyHash, fingerprint := automationIdempotency(delivery, stored.Automation.Trigger.AuthMode, event)
	if len(idempotencyHash) > 0 {
		existing, err := scanAutomationRun(tx.QueryRow(ctx, `SELECT `+automationRunColumns+`
      FROM automation_runs WHERE automation_id = $1 AND idempotency_hash = $2`, stored.Automation.ID, idempotencyHash))
		if err == nil {
			if !strings.EqualFold(existing.PayloadSHA256, hex.EncodeToString(payloadHash[:])) {
				return platform.AutomationTriggerResult{}, ErrAutomationIdempotencyConflict
			}
			return s.automationTriggerResult(existing, true), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return platform.AutomationTriggerResult{}, fmt.Errorf("check automation idempotency: %w", err)
		}
	}

	var recent, inflight int
	if err := tx.QueryRow(ctx, `SELECT
      COUNT(*) FILTER (WHERE received_at >= $2),
		COUNT(*) FILTER (WHERE status IN ('evaluating', 'queued', 'provisioning'))
    FROM automation_runs WHERE automation_id = $1`, stored.Automation.ID, time.Now().UTC().Add(-time.Minute)).Scan(&recent, &inflight); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("check automation limits: %w", err)
	}
	if recent >= automationRequestsPerMinute || inflight >= automationInflightLimit {
		return platform.AutomationTriggerResult{}, ErrAutomationRateLimit
	}

	runID := uuid.NewString()
	templateResource, templateErr := loadAutomationTemplate(ctx, tx, stored.Automation.ProjectID, stored.Automation.TemplateID)
	templateName := stored.Automation.TemplateID
	if templateErr == nil {
		templateName = templateResource.Name
	}
	run := platform.AutomationRun{
		ID: runID, AutomationID: stringPointer(stored.Automation.ID),
		EndpointID: stored.Automation.EndpointID,
		ProjectID:  stored.Automation.ProjectID, AutomationName: stored.Automation.Name,
		TemplateID: stored.Automation.TemplateID, TemplateName: templateName,
		TriggerSource: triggerSource, AuthMode: stored.Automation.Trigger.AuthMode,
		Event:                  event,
		IdempotencyFingerprint: fingerprint, PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		PayloadBytes: len(delivery.Body), Status: platform.AutomationRunEvaluating,
		ReceivedAt: receivedAt, ErrorDetails: map[string]string{},
	}
	if err := insertAutomationRun(ctx, tx, run, idempotencyHash, payloadHash[:]); err != nil {
		return platform.AutomationTriggerResult{}, err
	}

	if templateErr != nil {
		if !platform.IsValidationError(templateErr) {
			return platform.AutomationTriggerResult{}, templateErr
		}
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
			"template_invalid", templateErr.Error(), receivedAt)
	}
	sandboxInput, encodedInput, buildErr := buildAutomatedSandboxInput(
		stored.Automation, runID, templateResource,
	)
	if buildErr == nil {
		buildErr = platform.Validate(sandboxInput)
	}
	if buildErr == nil {
		buildErr = s.ensureAutomatedSandboxReferences(ctx, tx, sandboxInput)
	}
	if buildErr == nil {
		sandboxInput.Spec["capabilityRevision"] = int64(1)
		buildErr = s.encryptSpecEnvironmentVariables(sandboxInput.Spec)
	}
	if buildErr != nil {
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
			"input_invalid", buildErr.Error(), receivedAt)
	}

	resource, err := scanResource(tx.QueryRow(ctx, `INSERT INTO control_resources
    (id, kind, project_id, name, description, enabled, spec, created_at, updated_at, observed_generation)
    VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $8, 0)
    RETURNING `+resourceColumns, sandboxInput.ID, sandboxInput.Kind, sandboxInput.ProjectID,
		sandboxInput.Name, sandboxInput.Description, sandboxInput.Enabled, mustMapJSON(sandboxInput.Spec), receivedAt,
	))
	if err != nil {
		return platform.AutomationTriggerResult{}, mapResourceError(err)
	}
	jobID, _, err := enqueueAutomationSandboxJob(ctx, tx, resource, runID)
	if err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	inputHash := sha256.Sum256(encodedInput)
	queuedAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'queued', sandbox_id = $1,
      worker_job_id = $2, input_sha256 = $3, queued_at = $4 WHERE id = $5`,
		resource.ID, jobID, inputHash[:], queuedAt, runID,
	); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("queue automation run: %w", err)
	}
	if err := updateAutomationLastTriggered(ctx, tx, stored.Automation.ID, receivedAt); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if err := s.appendAuditEvent(ctx, tx, automationRunAuditEntry(stored.Automation.ID, run,
		platform.AutomationRunQueued, "", jobID)); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("commit automation trigger: %w", err)
	}
	run.Status = platform.AutomationRunQueued
	run.SandboxID = stringPointer(resource.ID)
	run.WorkerJobID = stringPointer(jobID)
	run.InputSHA256 = hex.EncodeToString(inputHash[:])
	run.QueuedAt = &queuedAt
	return s.automationTriggerResult(run, false), nil
}

func (s *Store) ListAutomationRuns(ctx context.Context, projectID, automationID string, limit int) ([]platform.AutomationRun, error) {
	page, err := s.ListAutomationRunsPage(ctx, platform.AutomationRunFilter{
		ProjectID: projectID, AutomationID: automationID, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) ListAutomationRunsPage(ctx context.Context, filter platform.AutomationRunFilter) (platform.AutomationRunPage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	automationID := strings.TrimSpace(filter.AutomationID)
	if automationID != "" {
		if _, err := uuid.Parse(automationID); err != nil {
			return platform.AutomationRunPage{}, &platform.ValidationError{Message: "自动化筛选无效"}
		}
	}
	query := `SELECT ` + automationRunColumns + ` FROM automation_runs
		WHERE project_id = $1 AND action_type = 'create-sandbox'
		  AND status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')`
	args := []any{strings.TrimSpace(filter.ProjectID)}
	if automationID != "" {
		query += ` AND automation_id = $2`
		args = append(args, automationID)
	}
	if filter.Status != "" {
		switch filter.Status {
		case platform.AutomationRunEvaluating, platform.AutomationRunQueued,
			platform.AutomationRunProvisioning, platform.AutomationRunSucceeded, platform.AutomationRunFailed:
		default:
			return platform.AutomationRunPage{}, &platform.ValidationError{Message: "运行状态筛选无效"}
		}
		query += fmt.Sprintf(` AND status = $%d`, len(args)+1)
		args = append(args, filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query += fmt.Sprintf(` AND concat_ws(' ', automation_name, template_name, template_id,
			COALESCE(sandbox_id, ''), error_message) ILIKE $%d`, len(args)+1)
		args = append(args, "%"+search+"%")
	}
	if strings.TrimSpace(filter.Cursor) != "" {
		receivedAt, id, err := decodeAutomationRunCursor(filter.Cursor)
		if err != nil {
			return platform.AutomationRunPage{}, &platform.ValidationError{Message: "运行记录游标无效"}
		}
		query += fmt.Sprintf(` AND (received_at, id) < ($%d, $%d::uuid)`, len(args)+1, len(args)+2)
		args = append(args, receivedAt, id)
	}
	query += fmt.Sprintf(` ORDER BY received_at DESC, id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return platform.AutomationRunPage{}, fmt.Errorf("list automation runs: %w", err)
	}
	defer rows.Close()
	runs := make([]platform.AutomationRun, 0, limit+1)
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return platform.AutomationRunPage{}, fmt.Errorf("scan automation run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return platform.AutomationRunPage{}, err
	}
	page := platform.AutomationRunPage{Items: runs}
	if len(runs) > limit {
		page.Items = runs[:limit]
		page.HasMore = true
		page.NextCursor = encodeAutomationRunCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

func encodeAutomationRunCursor(run platform.AutomationRun) string {
	value := run.ReceivedAt.UTC().Format(time.RFC3339Nano) + "\n" + run.ID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeAutomationRunCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return time.Time{}, "", err
	}
	receivedAt, id, ok := strings.Cut(string(raw), "\n")
	if !ok {
		return time.Time{}, "", errors.New("automation run cursor has no separator")
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err != nil {
		return time.Time{}, "", err
	}
	if _, err := uuid.Parse(id); err != nil {
		return time.Time{}, "", err
	}
	return parsedTime, id, nil
}

func (s *Store) GetAutomationRun(ctx context.Context, id string) (platform.AutomationRun, error) {
	run, err := scanAutomationRun(s.pool.QueryRow(ctx, `SELECT `+automationRunColumns+`
	    FROM automation_runs WHERE id = $1 AND action_type = 'create-sandbox'
	      AND status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.AutomationRun{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.AutomationRun{}, fmt.Errorf("get automation run: %w", err)
	}
	return run, nil
}

func (s *Store) GetPublicAutomationRun(ctx context.Context, endpointID, runID, token string) (platform.AutomationRun, error) {
	if !hmac.Equal([]byte(strings.TrimSpace(token)), []byte(s.automationStatusToken(endpointID, runID))) {
		return platform.AutomationRun{}, ErrWebhookUnauthorized
	}
	run, err := scanAutomationRun(s.pool.QueryRow(ctx, `SELECT `+automationRunColumns+`
		FROM automation_runs WHERE id = $1 AND endpoint_id = $2 AND action_type = 'create-sandbox'
		  AND status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')`, runID, endpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.AutomationRun{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.AutomationRun{}, fmt.Errorf("get public automation run: %w", err)
	}
	return run, nil
}

func insertAutomationRun(ctx context.Context, tx pgx.Tx, run platform.AutomationRun, idempotencyHash, payloadHash []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO automation_runs
	(id, automation_id, project_id, automation_name, endpoint_id,
	 template_id, template_name, trigger_source, auth_mode, event_id, event_type,
	 event_source, event_time, idempotency_hash, idempotency_fingerprint,
	 payload_sha256, payload_bytes, status, error_code, error_message, received_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
	  $14, $15, $16, $17, $18, '', '', $19)`,
		run.ID, run.AutomationID, run.ProjectID, run.AutomationName, run.EndpointID,
		run.TemplateID, run.TemplateName, run.TriggerSource, run.AuthMode,
		run.Event.ID, run.Event.Type, run.Event.Source, run.Event.Time, nullableBytes(idempotencyHash),
		run.IdempotencyFingerprint, payloadHash, run.PayloadBytes, run.Status, run.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("insert automation run: %w", err)
	}
	return nil
}

func (s *Store) finishAutomationRun(
	ctx context.Context,
	tx pgx.Tx,
	automationID string,
	run platform.AutomationRun,
	status platform.AutomationRunStatus,
	code, message string,
	triggeredAt time.Time,
) (platform.AutomationTriggerResult, error) {
	finishedAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = $1, error_code = $2,
		error_message = $3, finished_at = $4 WHERE id = $5`, status, code, message, finishedAt, run.ID); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("finish automation run: %w", err)
	}
	if err := updateAutomationLastTriggered(ctx, tx, automationID, triggeredAt); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if err := s.appendAuditEvent(ctx, tx, automationRunAuditEntry(automationID, run, status, code, "")); err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("commit finished automation run: %w", err)
	}
	run.Status = status
	run.ErrorCode = code
	run.ErrorMessage = message
	run.FinishedAt = &finishedAt
	return s.automationTriggerResult(run, false), nil
}

func automationRunAuditEntry(automationID string, run platform.AutomationRun, status platform.AutomationRunStatus, code, jobID string) platform.LogEntry {
	action := "webhook"
	if run.TriggerSource == "manual-test" {
		action = "test"
	}
	entry := platform.LogEntry{
		Category: platform.LogCategoryAutomation, Action: action,
		ResourceKind: "automation", ResourceID: automationID, ResourceName: run.AutomationName,
		Detail: map[string]any{"runId": run.ID, "status": string(status), "triggerSource": run.TriggerSource,
			"projectId": run.ProjectID, "templateId": run.TemplateID, "errorCode": code, "jobId": jobID},
	}
	if status == platform.AutomationRunFailed {
		entry.Status = platform.LogStatusFailed
		entry.Level = platform.LogLevelWarn
	}
	return entry
}

func updateAutomationLastTriggered(ctx context.Context, tx pgx.Tx, automationID string, triggeredAt time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE automations SET last_triggered_at = $1 WHERE id = $2`, triggeredAt, automationID); err != nil {
		return fmt.Errorf("update automation trigger time: %w", err)
	}
	return nil
}

func (s *Store) automationTriggerResult(run platform.AutomationRun, duplicate bool) platform.AutomationTriggerResult {
	return platform.AutomationTriggerResult{
		Run: run, Duplicate: duplicate, StatusToken: s.automationStatusToken(run.EndpointID, run.ID),
	}
}

func (s *Store) automationStatusToken(endpointID, runID string) string {
	mac := hmac.New(sha256.New, s.secretKey)
	_, _ = mac.Write([]byte("automation-run:" + endpointID + ":" + runID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func buildAutomatedSandboxInput(
	automation platform.Automation,
	runID string,
	templateResource platform.Resource,
) (platform.Input, []byte, error) {
	shortID := strings.ReplaceAll(runID, "-", "")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	encodedSpec, err := json.Marshal(templateResource.Spec)
	if err != nil {
		return platform.Input{}, nil, fmt.Errorf("编码沙箱模板失败: %w", err)
	}
	spec := map[string]any{}
	if err := json.Unmarshal(encodedSpec, &spec); err != nil {
		return platform.Input{}, nil, fmt.Errorf("读取沙箱模板失败: %w", err)
	}
	spec["runtimeId"] = templateResource.ID
	spec["policy"] = "new"
	spec["status"] = "requested"
	spec["automationId"] = automation.ID
	spec["automationRunId"] = runID
	spec["modelBindings"] = automation.ModelBindings
	input := platform.Input{
		ID:          "auto-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Kind:        platform.KindSandbox,
		ProjectID:   stringPointer(automation.ProjectID),
		Name:        automatedSandboxName(automation.Name, shortID),
		Description: "由自动化 " + automation.Name + " 创建",
		Enabled:     true,
		Spec:        spec,
	}
	platform.Normalize(&input)
	encoded, err := json.Marshal(input)
	if err != nil {
		return platform.Input{}, nil, fmt.Errorf("编码沙箱输入失败: %w", err)
	}
	return input, encoded, nil
}

func automatedSandboxName(automationName, shortID string) string {
	const maxRunes = 80
	suffix := []rune("-" + shortID)
	name := []rune(automationName)
	if len(name)+len(suffix) > maxRunes {
		name = name[:maxRunes-len(suffix)]
	}
	return string(name) + string(suffix)
}

func enqueueAutomationSandboxJob(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, runID string) (string, map[string]any, error) {
	payload, driver, imageReference, err := buildSandboxJobPayload(ctx, tx, sandbox, true)
	if err != nil {
		return "", nil, err
	}
	if err := persistSandboxCreationSpec(ctx, tx, sandbox, payload, driver, imageReference); err != nil {
		return "", nil, err
	}
	payload["driver"] = driver
	now := time.Now().UTC()
	jobID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
		(id, server_id, resource_id, action, status, payload, automation_run_id, created_at, updated_at, resource_generation)
		VALUES ($1, $2, $3, 'create-sandbox', 'pending', $4::jsonb, $5, $6, $6, $7)`,
		jobID, sandbox.Spec["serverId"], sandbox.ID, mustMapJSON(payload), runID, now, sandbox.Generation); err != nil {
		return "", nil, fmt.Errorf("enqueue automated sandbox creation: %w", err)
	}
	return jobID, payload, nil
}

func (s *Store) ensureAutomatedSandboxReferences(ctx context.Context, tx pgx.Tx, input platform.Input) error {
	runtimeID, _ := input.Spec["runtimeId"].(string)
	var runtimeSpecJSON []byte
	if err := tx.QueryRow(ctx, `SELECT spec FROM control_resources
      WHERE id = $1 AND kind = 'runtime' AND project_id = $2 AND enabled = TRUE`, runtimeID, input.ProjectID).Scan(&runtimeSpecJSON); errors.Is(err, pgx.ErrNoRows) {
		return &platform.ValidationError{Message: "环境模板不存在、未启用或不属于当前 Project"}
	} else if err != nil {
		return fmt.Errorf("check automated sandbox runtime: %w", err)
	}
	var runtimeSpec map[string]any
	if err := json.Unmarshal(runtimeSpecJSON, &runtimeSpec); err != nil {
		return fmt.Errorf("decode automated sandbox environment: %w", err)
	}
	effectiveSpec := effectiveSandboxSpec(runtimeSpec, input.Spec)
	driver, _ := effectiveSpec["driver"].(string)
	network, _ := effectiveSpec["network"].(string)
	effectiveSpec["network"] = platform.EffectiveNetworkPolicy(driver, network)
	if err := s.validateNetworkProxyBinding(ctx, tx, effectiveSpec, "自动化沙箱"); err != nil {
		return err
	}
	if err := ensureExtensionReferences(ctx, tx, input.ProjectID, effectiveSpec, true); err != nil {
		return err
	}
	serverID, _ := effectiveSpec["serverId"].(string)
	requiredCapability := driver
	if driver == "vm" {
		requiredCapability = "kvm"
	}
	var supports bool
	var serverArch string
	var inventoryJSON []byte
	var serverOnline bool
	if err := tx.QueryRow(ctx, `SELECT capabilities ? $2, arch, inventory,
      last_seen_at > NOW() - INTERVAL '45 seconds'
      FROM managed_servers WHERE id = $1`, serverID, requiredCapability).Scan(&supports, &serverArch, &inventoryJSON, &serverOnline); errors.Is(err, pgx.ErrNoRows) {
		return &platform.ValidationError{Message: "目标服务器不存在"}
	} else if err != nil {
		return fmt.Errorf("check automated sandbox server: %w", err)
	}
	if !serverOnline {
		return &platform.ValidationError{Message: "目标服务器离线，无法创建沙箱"}
	}
	if !supports {
		return &platform.ValidationError{Message: "目标服务器尚未通过所选隔离驱动的自检"}
	}
	imageReference, _ := effectiveSpec["imageReference"].(string)
	if imageReference == "" {
		imageID, _ := effectiveSpec["imageId"].(string)
		if err := tx.QueryRow(ctx, `SELECT spec->>'reference' FROM control_resources
          WHERE id = $1 AND kind = 'image' AND enabled = TRUE`, imageID).Scan(&imageReference); err != nil {
			return &platform.ValidationError{Message: "沙箱引用的镜像不存在"}
		}
	}
	var inventory platform.ServerInventory
	if err := json.Unmarshal(inventoryJSON, &inventory); err != nil {
		return fmt.Errorf("decode automated sandbox server inventory: %w", err)
	}
	if !runtimeImageIsAvailable(driver, inventory, imageReference, serverArch) {
		return &platform.ValidationError{Message: "沙箱使用的镜像已不在目标服务器上"}
	}
	if _, err := ensureCapabilityReferences(ctx, tx, input.ProjectID, effectiveSpec, "自动化沙箱"); err != nil {
		return err
	}
	allowedCredentialIDs := specStringList(effectiveSpec, "credentialIds")
	modelBindings := specStringMap(effectiveSpec, "modelBindings")
	if err := validateAutomationModelBindings(ctx, tx, effectiveSpec, modelBindings); err != nil {
		return err
	}
	input.Spec["credentialIds"] = allowedCredentialIDs
	return nil
}

func validateAutomationModelBindings(
	ctx context.Context,
	queryer automationQueryRow,
	spec map[string]any,
	modelBindings map[string]string,
) error {
	allowedCredentialIDs := specStringList(spec, "credentialIds")
	if len(modelBindings) != len(allowedCredentialIDs) {
		return &platform.ValidationError{Message: "请为沙箱中的每个模型服务选择具体模型"}
	}
	allowedCredentials := make(map[string]bool, len(allowedCredentialIDs))
	credentialProtocols := make(map[string]bool, len(allowedCredentialIDs))
	for _, credentialID := range allowedCredentialIDs {
		allowedCredentials[credentialID] = true
		modelID := strings.TrimSpace(modelBindings[credentialID])
		if modelID == "" {
			return &platform.ValidationError{Message: "请为沙箱中的每个模型服务选择具体模型"}
		}
		var protocol string
		if err := queryer.QueryRow(ctx, `SELECT protocol FROM provider_credentials
        WHERE id = $1 AND enabled = TRUE
          AND models @> jsonb_build_array(jsonb_build_object('id', $2::text))`, credentialID, modelID).Scan(&protocol); errors.Is(err, pgx.ErrNoRows) {
			return &platform.ValidationError{Message: "所选模型不存在、已停用或模型列表已更新"}
		} else if err != nil {
			return fmt.Errorf("check automated sandbox model binding: %w", err)
		}
		credentialProtocols[protocol] = true
	}
	for credentialID := range modelBindings {
		if !allowedCredentials[credentialID] {
			return &platform.ValidationError{Message: "所选模型服务不属于当前沙箱"}
		}
	}
	if incompatible := incompatibleAgentTools(specStringList(spec, "agentTools"), credentialProtocols); len(incompatible) > 0 {
		return &platform.ValidationError{Message: strings.Join(incompatible, "、") + " 与当前所选模型服务的接口协议不兼容"}
	}
	return nil
}

type automationQueryRow interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadAutomationTemplate(ctx context.Context, queryer automationQueryRow, projectID, templateID string) (platform.Resource, error) {
	templateResource, err := scanResource(queryer.QueryRow(ctx, `SELECT `+resourceColumns+`
    FROM control_resources WHERE id = $1 AND kind = 'runtime' AND project_id = $2 AND enabled = TRUE`, templateID, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Resource{}, &platform.ValidationError{Message: "沙箱模板不存在、未启用或不属于当前项目"}
	}
	if err != nil {
		return platform.Resource{}, fmt.Errorf("load automation sandbox template: %w", err)
	}
	return templateResource, nil
}

func (s *Store) verifyAutomationDelivery(stored storedAutomation, delivery platform.AutomationDelivery) error {
	switch stored.Automation.Trigger.AuthMode {
	case platform.AutomationAuthBearer:
		prefix, token, found := strings.Cut(strings.TrimSpace(delivery.Authorization), " ")
		if !found || !strings.EqualFold(prefix, "Bearer") || token == "" || !hmac.Equal(hashToken(token), stored.SecretHash) {
			return ErrWebhookUnauthorized
		}
		return nil
	case platform.AutomationAuthHMAC:
		timestamp, err := strconv.ParseInt(strings.TrimSpace(delivery.Timestamp), 10, 64)
		if err != nil || !webhookTimestampIsFresh(timestamp, time.Now()) {
			return ErrWebhookUnauthorized
		}
		provided := strings.TrimPrefix(strings.TrimSpace(delivery.Signature), "v1=")
		providedBytes, err := base64.RawURLEncoding.DecodeString(provided)
		if err != nil {
			return ErrWebhookUnauthorized
		}
		secret, err := decryptSecret(s.secretKey, stored.SecretCiphertext, stored.SecretNonce)
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
		_, _ = mac.Write(delivery.Body)
		if !hmac.Equal(providedBytes, mac.Sum(nil)) {
			return ErrWebhookUnauthorized
		}
		return nil
	case platform.AutomationAuthGitHub:
		provided := strings.TrimPrefix(strings.TrimSpace(automationHeader(delivery.Headers, "x-hub-signature-256")), "sha256=")
		providedBytes, err := hex.DecodeString(provided)
		if err != nil || len(providedBytes) == 0 {
			return ErrWebhookUnauthorized
		}
		secret, err := decryptSecret(s.secretKey, stored.SecretCiphertext, stored.SecretNonce)
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(delivery.Body)
		if !hmac.Equal(providedBytes, mac.Sum(nil)) {
			return ErrWebhookUnauthorized
		}
		return nil
	case platform.AutomationAuthGitLab:
		provided := strings.TrimSpace(automationHeader(delivery.Headers, "x-gitlab-token"))
		if provided == "" || !hmac.Equal(hashToken(provided), stored.SecretHash) {
			return ErrWebhookUnauthorized
		}
		return nil
	case platform.AutomationAuthStandardWebhook:
		timestampValue := strings.TrimSpace(automationHeader(delivery.Headers, "webhook-timestamp"))
		timestamp, err := strconv.ParseInt(timestampValue, 10, 64)
		if err != nil || !webhookTimestampIsFresh(timestamp, time.Now()) {
			return ErrWebhookUnauthorized
		}
		deliveryID := strings.TrimSpace(automationHeader(delivery.Headers, "webhook-id"))
		if deliveryID == "" {
			return ErrWebhookUnauthorized
		}
		secret, err := decryptSecret(s.secretKey, stored.SecretCiphertext, stored.SecretNonce)
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, standardWebhookSecret(secret))
		_, _ = mac.Write([]byte(deliveryID + "." + timestampValue + "."))
		_, _ = mac.Write(delivery.Body)
		expected := mac.Sum(nil)
		for signature := range strings.FieldsSeq(automationHeader(delivery.Headers, "webhook-signature")) {
			version, encoded, found := strings.Cut(signature, ",")
			if !found || version != "v1" {
				continue
			}
			provided, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil {
				provided, decodeErr = base64.RawStdEncoding.DecodeString(encoded)
			}
			if decodeErr == nil && hmac.Equal(provided, expected) {
				return nil
			}
		}
		return ErrWebhookUnauthorized
	default:
		return ErrWebhookUnauthorized
	}
}

func standardWebhookSecret(secret string) []byte {
	encoded := strings.TrimPrefix(secret, "whsec_")
	if encoded == secret {
		return []byte(secret)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) == 0 {
		return []byte(secret)
	}
	return decoded
}

func automationHeader(headers map[string]string, key string) string {
	if value, ok := headers[key]; ok {
		return value
	}
	for candidate, value := range headers {
		if strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}

func canonicalAutomationEvent(delivery platform.AutomationDelivery, mode platform.AutomationAuthMode, receivedAt time.Time) platform.AutomationEvent {
	event := platform.AutomationEvent{Source: "generic", ReceivedAt: receivedAt, Time: receivedAt}
	switch mode {
	case platform.AutomationAuthGitHub:
		event.Source = "github"
		event.ID = strings.TrimSpace(automationHeader(delivery.Headers, "x-github-delivery"))
		event.Type = strings.TrimSpace(automationHeader(delivery.Headers, "x-github-event"))
	case platform.AutomationAuthGitLab:
		event.Source = "gitlab"
		event.ID = strings.TrimSpace(automationHeader(delivery.Headers, "x-gitlab-event-uuid"))
		event.Type = strings.TrimSpace(automationHeader(delivery.Headers, "x-gitlab-event"))
	case platform.AutomationAuthStandardWebhook:
		event.Source = "standard-webhooks"
		event.ID = strings.TrimSpace(automationHeader(delivery.Headers, "webhook-id"))
		event.Type = strings.TrimSpace(automationHeader(delivery.Headers, "webhook-type"))
		if timestamp, err := strconv.ParseInt(strings.TrimSpace(automationHeader(delivery.Headers, "webhook-timestamp")), 10, 64); err == nil {
			event.Time = time.Unix(timestamp, 0).UTC()
		}
	default:
		event.ID = strings.TrimSpace(automationHeader(delivery.Headers, "ce-id"))
		event.Type = strings.TrimSpace(automationHeader(delivery.Headers, "ce-type"))
		if source := strings.TrimSpace(automationHeader(delivery.Headers, "ce-source")); source != "" {
			event.Source = source
		}
		if value := strings.TrimSpace(automationHeader(delivery.Headers, "ce-time")); value != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				event.Time = parsed.UTC()
			}
		}
	}
	if event.ID == "" {
		event.ID = strings.TrimSpace(delivery.IdempotencyKey)
	}
	if event.ID == "" {
		event.ID = strings.TrimSpace(automationHeader(delivery.Headers, "x-event-id"))
	}
	if event.Type == "" {
		event.Type = strings.TrimSpace(automationHeader(delivery.Headers, "x-event-type"))
	}
	if event.Type == "" {
		event.Type = "event"
	}
	return event
}

func validateAutomationIdempotencyKey(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 255 || strings.IndexFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return &platform.ValidationError{Message: "Idempotency-Key 必须是最多 255 字节的可打印文本"}
	}
	return nil
}

func automationIdempotency(delivery platform.AutomationDelivery, mode platform.AutomationAuthMode, event platform.AutomationEvent) ([]byte, string) {
	value := strings.TrimSpace(delivery.IdempotencyKey)
	if value == "" {
		value = strings.TrimSpace(event.ID)
	}
	if value == "" && mode == platform.AutomationAuthHMAC {
		value = strings.TrimSpace(delivery.Signature)
	}
	if value == "" {
		return nil, ""
	}
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	return sum[:], encoded[:12]
}

func scanAutomation(row pgx.Row) (platform.Automation, error) {
	var result platform.Automation
	var triggerType string
	var createdBy, updatedBy pgtype.Text
	var modelBindingsJSON []byte
	err := row.Scan(
		&result.ID, &result.ProjectID, &result.Name, &result.Description, &result.Enabled,
		&triggerType, &result.Trigger.AuthMode, &result.EndpointID, &result.SecretLastFour,
		&result.TemplateID, &modelBindingsJSON, &createdBy, &updatedBy,
		&result.LastTriggeredAt, &result.SecretRotatedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	result.Trigger.Type = triggerType
	if err == nil {
		err = json.Unmarshal(modelBindingsJSON, &result.ModelBindings)
		result.SecretLastFour = secretMask
	}
	if createdBy.Valid {
		result.CreatedBy = &createdBy.String
	}
	if updatedBy.Valid {
		result.UpdatedBy = &updatedBy.String
	}
	return result, err
}

func scanStoredAutomation(row pgx.Row) (storedAutomation, error) {
	var result storedAutomation
	var triggerType string
	var createdBy, updatedBy pgtype.Text
	var modelBindingsJSON []byte
	err := row.Scan(
		&result.Automation.ID, &result.Automation.ProjectID, &result.Automation.Name,
		&result.Automation.Description, &result.Automation.Enabled, &triggerType,
		&result.Automation.Trigger.AuthMode, &result.Automation.EndpointID,
		&result.Automation.SecretLastFour, &result.Automation.TemplateID,
		&modelBindingsJSON, &createdBy, &updatedBy,
		&result.Automation.LastTriggeredAt, &result.Automation.SecretRotatedAt,
		&result.Automation.CreatedAt, &result.Automation.UpdatedAt,
		&result.SecretHash, &result.SecretCiphertext, &result.SecretNonce,
	)
	result.Automation.Trigger.Type = triggerType
	if err == nil {
		err = json.Unmarshal(modelBindingsJSON, &result.Automation.ModelBindings)
		result.Automation.SecretLastFour = secretMask
	}
	if createdBy.Valid {
		result.Automation.CreatedBy = &createdBy.String
	}
	if updatedBy.Valid {
		result.Automation.UpdatedBy = &updatedBy.String
	}
	return result, err
}

func automationModelBindingsJSON(value map[string]string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func scanAutomationRun(row pgx.Row) (platform.AutomationRun, error) {
	var result platform.AutomationRun
	var automationID, sandboxID, workerJobID pgtype.Text
	var eventTime pgtype.Timestamptz
	var errorDetailsJSON, provisioningJSON []byte
	err := row.Scan(
		&result.ID, &automationID, &result.ProjectID, &result.AutomationName,
		&result.EndpointID, &result.TemplateID, &result.TemplateName,
		&result.TriggerSource, &result.AuthMode, &result.Event.ID, &result.Event.Type,
		&result.Event.Source, &eventTime,
		&result.IdempotencyFingerprint, &result.PayloadSHA256, &result.PayloadBytes,
		&result.InputSHA256, &result.Status, &sandboxID, &workerJobID,
		&result.ErrorCode, &result.ErrorMessage, &result.ErrorStage, &result.ErrorRetryable,
		&errorDetailsJSON, &result.ReceivedAt, &result.QueuedAt,
		&result.StartedAt, &result.FinishedAt, &provisioningJSON,
	)
	if err == nil {
		err = json.Unmarshal(errorDetailsJSON, &result.ErrorDetails)
	}
	if err == nil {
		err = json.Unmarshal(provisioningJSON, &result.Provisioning)
	}
	if automationID.Valid {
		result.AutomationID = &automationID.String
	}
	if sandboxID.Valid {
		result.SandboxID = &sandboxID.String
	}
	if workerJobID.Valid {
		result.WorkerJobID = &workerJobID.String
	}
	if eventTime.Valid {
		result.Event.Time = eventTime.Time
	} else {
		result.Event.Time = result.ReceivedAt
	}
	result.Event.ReceivedAt = result.ReceivedAt
	return result, err
}

func newWebhookSecret() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	return "abx_wh_" + token, nil
}

func webhookTimestampIsFresh(timestamp int64, now time.Time) bool {
	signedAt := time.Unix(timestamp, 0)
	return !signedAt.Before(now.Add(-webhookSignatureWindow)) &&
		!signedAt.After(now.Add(webhookSignatureWindow))
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func stringPointer(value string) *string { return &value }
