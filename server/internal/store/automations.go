package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
  trigger_type, action_type, auth_mode, endpoint_id::text, secret_last_four,
  COALESCE(template_id, ''), model_bindings, input_template, condition_template,
  target_template, command_template, timeout_seconds, cleanup_policy, expires_after_seconds,
  created_by::text, updated_by::text,
  last_triggered_at, secret_rotated_at, created_at, updated_at`

const automationRunColumns = `id::text, automation_id::text, project_id, automation_name,
  COALESCE(endpoint_id::text, ''), action_type, template_id, template_name,
  trigger_source, auth_mode, event_id, event_type, event_source, event_time,
  idempotency_fingerprint,
  encode(payload_sha256, 'hex'), payload_bytes, COALESCE(encode(input_sha256, 'hex'), ''),
  status, sandbox_id, worker_job_id::text, exit_code, output, output_truncated,
  cleanup_status, error_code, error_message, received_at, queued_at, started_at,
  finished_at, expires_at`

type storedAutomation struct {
	Automation       platform.Automation
	SecretHash       []byte
	SecretCiphertext []byte
	SecretNonce      []byte
}

func (s *Store) ListAutomations(ctx context.Context, projectID string) ([]platform.Automation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+automationColumns+` FROM automations
    WHERE project_id = $1 ORDER BY enabled DESC, updated_at DESC`, strings.TrimSpace(projectID))
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
    FROM automations WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, fmt.Errorf("get automation: %w", err)
	}
	return automation, nil
}

func (s *Store) CreateAutomation(ctx context.Context, input platform.AutomationInput, userID string) (platform.Automation, string, error) {
	platform.NormalizeAutomationInput(&input)
	if err := platform.ValidateAutomationInput(input); err != nil {
		return platform.Automation{}, "", err
	}
	if err := validateAutomationInputTemplates(input); err != nil {
		return platform.Automation{}, "", &platform.ValidationError{Message: err.Error()}
	}
	if input.Action.Type != "destroy-sandbox" {
		if _, err := loadAutomationTemplate(ctx, s.pool, input.ProjectID, input.Action.TemplateID); err != nil {
			return platform.Automation{}, "", err
		}
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
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `INSERT INTO automations
    (id, project_id, name, description, enabled, trigger_type, action_type, auth_mode,
     endpoint_id, secret_hash, secret_ciphertext, secret_nonce, secret_last_four,
     template_id, model_bindings, input_template, condition_template, target_template,
     command_template, timeout_seconds, cleanup_policy, expires_after_seconds,
     created_by, updated_by, secret_rotated_at, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, 'webhook', $6, $7, $8, $9, $10, $11,
      $12, NULLIF($13, ''), $14, $15, $16, $17, $18, $19, $20, $21,
      $22, $22, $23, $23, $23)
    RETURNING `+automationColumns,
		uuid.NewString(), input.ProjectID, input.Name, input.Description, input.Enabled,
		input.Action.Type, input.Trigger.AuthMode, uuid.NewString(), hashToken(secret), ciphertext, nonce,
		lastFour(secret), input.Action.TemplateID, automationModelBindingsJSON(input.Action.ModelBindings),
		input.Action.InputTemplate, input.ConditionTemplate, input.Action.TargetTemplate,
		input.Action.CommandTemplate, input.Action.TimeoutSeconds, input.Action.CleanupPolicy,
		input.Action.ExpiresAfterSeconds, userID, now,
	))
	if err != nil {
		return platform.Automation{}, "", mapResourceError(err)
	}
	return automation, secret, nil
}

func (s *Store) UpdateAutomation(ctx context.Context, id string, input platform.AutomationInput, userID string) (platform.Automation, error) {
	platform.NormalizeAutomationInput(&input)
	if err := platform.ValidateAutomationInput(input); err != nil {
		return platform.Automation{}, err
	}
	if err := validateAutomationInputTemplates(input); err != nil {
		return platform.Automation{}, &platform.ValidationError{Message: err.Error()}
	}
	if input.Action.Type != "destroy-sandbox" {
		if _, err := loadAutomationTemplate(ctx, s.pool, input.ProjectID, input.Action.TemplateID); err != nil {
			return platform.Automation{}, err
		}
	}
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `UPDATE automations SET
	project_id = $1, name = $2, description = $3, enabled = $4, action_type = $5,
	auth_mode = $6, template_id = NULLIF($7, ''), model_bindings = $8,
	input_template = $9, condition_template = $10, target_template = $11,
	command_template = $12, timeout_seconds = $13, cleanup_policy = $14,
	expires_after_seconds = $15, updated_by = $16, updated_at = $17
	WHERE id = $18 RETURNING `+automationColumns,
		input.ProjectID, input.Name, input.Description, input.Enabled, input.Action.Type,
		input.Trigger.AuthMode, input.Action.TemplateID, automationModelBindingsJSON(input.Action.ModelBindings),
		input.Action.InputTemplate, input.ConditionTemplate, input.Action.TargetTemplate,
		input.Action.CommandTemplate, input.Action.TimeoutSeconds, input.Action.CleanupPolicy,
		input.Action.ExpiresAfterSeconds, userID, time.Now().UTC(), id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, mapResourceError(err)
	}
	return automation, nil
}

func (s *Store) DeleteAutomation(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, "DELETE FROM automations WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete automation: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrResourceNotFound
	}
	return nil
}

func (s *Store) RotateAutomationSecret(ctx context.Context, id, userID string) (platform.Automation, string, error) {
	secret, err := newWebhookSecret()
	if err != nil {
		return platform.Automation{}, "", err
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, secret)
	if err != nil {
		return platform.Automation{}, "", err
	}
	now := time.Now().UTC()
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `UPDATE automations SET
    secret_hash = $1, secret_ciphertext = $2, secret_nonce = $3, secret_last_four = $4,
    updated_by = $5, secret_rotated_at = $6, updated_at = $6
    WHERE id = $7 RETURNING `+automationColumns,
		hashToken(secret), ciphertext, nonce, lastFour(secret), userID, now, id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Automation{}, "", ErrResourceNotFound
	}
	if err != nil {
		return platform.Automation{}, "", fmt.Errorf("rotate automation secret: %w", err)
	}
	return automation, secret, nil
}

func (s *Store) PreviewAutomation(ctx context.Context, input platform.AutomationPreviewInput) (platform.AutomationPreview, error) {
	platform.NormalizeAutomationInput(&input.Automation)
	if err := platform.ValidateAutomationInput(input.Automation); err != nil {
		return platform.AutomationPreview{}, err
	}
	if err := validateAutomationInputTemplates(input.Automation); err != nil {
		return platform.AutomationPreview{}, &platform.ValidationError{Message: err.Error()}
	}
	now := time.Now().UTC()
	event := canonicalAutomationEvent(platform.AutomationDelivery{Headers: input.Headers}, input.Automation.Trigger.AuthMode, now)
	runID := uuid.NewString()
	context := automationTemplateContext(input.Automation, "preview", runID, platform.Resource{}, input.Payload,
		filteredAutomationHeaders(input.Headers), input.Query, event)
	matched, err := renderAutomationCondition(input.Automation.ConditionTemplate, context)
	if err != nil {
		return platform.AutomationPreview{}, &platform.ValidationError{Message: err.Error()}
	}
	if !matched {
		return platform.AutomationPreview{Matched: false}, nil
	}
	if input.Automation.Action.Type == "destroy-sandbox" {
		target, err := renderAutomationString("目标模板", input.Automation.Action.TargetTemplate, context)
		if err != nil || target == "" {
			if err == nil {
				err = errors.New("目标模板渲染结果不能为空")
			}
			return platform.AutomationPreview{}, &platform.ValidationError{Message: err.Error()}
		}
		return platform.AutomationPreview{Matched: true, Target: target}, nil
	}
	templateResource, err := loadAutomationTemplate(ctx, s.pool, input.Automation.ProjectID, input.Automation.Action.TemplateID)
	if err != nil {
		return platform.AutomationPreview{}, err
	}
	sandboxInput, encoded, err := buildAutomatedSandboxInput(
		input.Automation, "preview", runID, templateResource, input.Payload,
		filteredAutomationHeaders(input.Headers), input.Query, event,
	)
	if err != nil {
		return platform.AutomationPreview{}, &platform.ValidationError{Message: err.Error()}
	}
	_ = encoded
	if err := platform.Validate(sandboxInput); err != nil {
		return platform.AutomationPreview{}, err
	}
	if err := s.ensureResourceReferences(ctx, sandboxInput, true); err != nil {
		return platform.AutomationPreview{}, err
	}
	preview := platform.AutomationPreview{Matched: true, Input: &platform.ResourceInputPreview{
		ID: sandboxInput.ID, Kind: sandboxInput.Kind, ProjectID: sandboxInput.ProjectID,
		Name: sandboxInput.Name, Description: sandboxInput.Description,
		Enabled: sandboxInput.Enabled, Spec: sandboxInput.Spec,
	}}
	if input.Automation.Action.Type == "run-task" {
		context = automationTemplateContext(input.Automation, "preview", runID, templateResource, input.Payload,
			filteredAutomationHeaders(input.Headers), input.Query, event)
		preview.Command, err = renderAutomationString("任务命令模板", input.Automation.Action.CommandTemplate, context)
		if err != nil || preview.Command == "" {
			if err == nil {
				err = errors.New("任务命令模板渲染结果不能为空")
			}
			return platform.AutomationPreview{}, &platform.ValidationError{Message: err.Error()}
		}
	}
	return preview, nil
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
	payload, err := decodeAutomationPayload(delivery.Body)
	if err != nil {
		return platform.AutomationTriggerResult{}, &platform.ValidationError{Message: err.Error()}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("begin automation trigger: %w", err)
	}
	defer tx.Rollback(ctx)
	where := "id = $1"
	if byEndpoint {
		where = "endpoint_id = $1"
	}
	stored, err := scanStoredAutomation(tx.QueryRow(ctx, `SELECT `+automationColumns+`,
    secret_hash, secret_ciphertext, secret_nonce FROM automations WHERE `+where+` FOR UPDATE`, identifier))
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
		COUNT(*) FILTER (WHERE status IN ('evaluating', 'queued', 'provisioning', 'running'))
    FROM automation_runs WHERE automation_id = $1`, stored.Automation.ID, time.Now().UTC().Add(-time.Minute)).Scan(&recent, &inflight); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("check automation limits: %w", err)
	}
	if recent >= automationRequestsPerMinute || inflight >= automationInflightLimit {
		return platform.AutomationTriggerResult{}, ErrAutomationRateLimit
	}

	runID := uuid.NewString()
	templateResource := platform.Resource{}
	var templateErr error
	if stored.Automation.Action.Type != "destroy-sandbox" {
		templateResource, templateErr = loadAutomationTemplate(ctx, tx, stored.Automation.ProjectID, stored.Automation.Action.TemplateID)
	}
	templateName := stored.Automation.Action.TemplateID
	if templateErr == nil {
		templateName = templateResource.Name
	}
	run := platform.AutomationRun{
		ID: runID, AutomationID: stringPointer(stored.Automation.ID),
		EndpointID: stored.Automation.EndpointID,
		ProjectID:  stored.Automation.ProjectID, AutomationName: stored.Automation.Name,
		ActionType: stored.Automation.Action.Type,
		TemplateID: stored.Automation.Action.TemplateID, TemplateName: templateName,
		TriggerSource: triggerSource, AuthMode: stored.Automation.Trigger.AuthMode,
		Event:                  event,
		IdempotencyFingerprint: fingerprint, PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		PayloadBytes: len(delivery.Body), Status: platform.AutomationRunEvaluating,
		ReceivedAt: receivedAt,
	}
	if stored.Automation.Action.ExpiresAfterSeconds > 0 {
		expiresAt := receivedAt.Add(time.Duration(stored.Automation.Action.ExpiresAfterSeconds) * time.Second)
		run.ExpiresAt = &expiresAt
	}
	if err := insertAutomationRun(ctx, tx, run, idempotencyHash, payloadHash[:]); err != nil {
		return platform.AutomationTriggerResult{}, err
	}

	context := automationTemplateContext(automationInputFromStored(stored.Automation), stored.Automation.ID,
		runID, templateResource, payload, filteredAutomationHeaders(delivery.Headers), delivery.Query, event)
	matched, conditionErr := renderAutomationCondition(stored.Automation.ConditionTemplate, context)
	if conditionErr != nil {
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
			"condition_invalid", conditionErr.Error(), receivedAt)
	}
	if !matched {
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunSkipped, "", "", receivedAt)
	}

	if stored.Automation.Action.Type == "destroy-sandbox" {
		targetID, renderErr := renderAutomationString("目标模板", stored.Automation.Action.TargetTemplate, context)
		if renderErr != nil || targetID == "" {
			if renderErr == nil {
				renderErr = errors.New("目标模板渲染结果不能为空")
			}
			return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
				"target_invalid", renderErr.Error(), receivedAt)
		}
		target, targetErr := scanResource(tx.QueryRow(ctx, `SELECT `+resourceColumns+`
			FROM control_resources WHERE id = $1 AND kind = 'sandbox' AND project_id = $2 FOR UPDATE`, targetID, stored.Automation.ProjectID))
		if errors.Is(targetErr, pgx.ErrNoRows) {
			return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
				"target_not_found", "目标沙箱不存在或不属于当前项目", receivedAt)
		}
		if targetErr != nil {
			return platform.AutomationTriggerResult{}, fmt.Errorf("load automation target sandbox: %w", targetErr)
		}
		jobID, enqueueErr := enqueueAutomationDeleteJob(ctx, tx, target, runID)
		if enqueueErr != nil {
			return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
				"target_invalid", enqueueErr.Error(), receivedAt)
		}
		queuedAt := time.Now().UTC()
		if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'queued', sandbox_id = $1,
			worker_job_id = $2, queued_at = $3 WHERE id = $4`, target.ID, jobID, queuedAt, runID); err != nil {
			return platform.AutomationTriggerResult{}, fmt.Errorf("queue destroy automation run: %w", err)
		}
		if err := updateAutomationLastTriggered(ctx, tx, stored.Automation.ID, receivedAt); err != nil {
			return platform.AutomationTriggerResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return platform.AutomationTriggerResult{}, fmt.Errorf("commit destroy automation trigger: %w", err)
		}
		run.Status = platform.AutomationRunQueued
		run.SandboxID = stringPointer(target.ID)
		run.WorkerJobID = stringPointer(jobID)
		run.QueuedAt = &queuedAt
		return s.automationTriggerResult(run, false), nil
	}

	if templateErr != nil {
		if !platform.IsValidationError(templateErr) {
			return platform.AutomationTriggerResult{}, templateErr
		}
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
			"template_invalid", templateErr.Error(), receivedAt)
	}
	sandboxInput, encodedInput, buildErr := buildAutomatedSandboxInput(
		automationInputFromStored(stored.Automation), stored.Automation.ID, runID,
		templateResource, payload, filteredAutomationHeaders(delivery.Headers), delivery.Query, event,
	)
	if buildErr == nil {
		buildErr = platform.Validate(sandboxInput)
	}
	if buildErr == nil {
		buildErr = ensureAutomatedSandboxReferences(ctx, tx, sandboxInput)
	}
	if buildErr == nil {
		buildErr = s.encryptSpecEnvironmentVariables(sandboxInput.Spec)
	}
	if buildErr != nil {
		return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
			"input_invalid", buildErr.Error(), receivedAt)
	}

	resource, err := scanResource(tx.QueryRow(ctx, `INSERT INTO control_resources
    (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $8)
    RETURNING `+resourceColumns, sandboxInput.ID, sandboxInput.Kind, sandboxInput.ProjectID,
		sandboxInput.Name, sandboxInput.Description, sandboxInput.Enabled, mustMapJSON(sandboxInput.Spec), receivedAt,
	))
	if err != nil {
		return platform.AutomationTriggerResult{}, mapResourceError(err)
	}
	jobID, createPayload, err := enqueueAutomationSandboxJob(ctx, tx, resource, runID)
	if err != nil {
		return platform.AutomationTriggerResult{}, err
	}
	if stored.Automation.Action.Type == "run-task" {
		command, renderErr := renderAutomationString("任务命令模板", stored.Automation.Action.CommandTemplate, context)
		if renderErr != nil || command == "" {
			if renderErr == nil {
				renderErr = errors.New("任务命令模板渲染结果不能为空")
			}
			return s.finishAutomationRun(ctx, tx, stored.Automation.ID, run, platform.AutomationRunFailed,
				"command_invalid", renderErr.Error(), receivedAt)
		}
		taskPayload := map[string]any{
			"sandboxId": resource.ID, "externalId": "", "driver": createPayload["driver"],
			"workdir": createPayload["workdir"], "command": command,
			"timeoutSeconds": stored.Automation.Action.TimeoutSeconds,
			"cleanupPolicy":  string(stored.Automation.Action.CleanupPolicy),
		}
		if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
			(id, server_id, resource_id, action, status, payload, automation_run_id, created_at, updated_at)
			VALUES ($1, $2, $3, 'run-sandbox-task', 'blocked', $4::jsonb, $5, $6, $6)`,
			uuid.NewString(), resource.Spec["serverId"], resource.ID, mustMapJSON(taskPayload), runID, receivedAt); err != nil {
			return platform.AutomationTriggerResult{}, fmt.Errorf("enqueue sandbox task: %w", err)
		}
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
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT ` + automationRunColumns + ` FROM automation_runs WHERE project_id = $1`
	args := []any{strings.TrimSpace(projectID)}
	if strings.TrimSpace(automationID) != "" {
		query += ` AND automation_id = $2`
		args = append(args, automationID)
	}
	query += fmt.Sprintf(` ORDER BY received_at DESC, id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list automation runs: %w", err)
	}
	defer rows.Close()
	runs := make([]platform.AutomationRun, 0)
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan automation run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) GetAutomationRun(ctx context.Context, id string) (platform.AutomationRun, error) {
	run, err := scanAutomationRun(s.pool.QueryRow(ctx, `SELECT `+automationRunColumns+`
    FROM automation_runs WHERE id = $1`, id))
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
		FROM automation_runs WHERE id = $1 AND endpoint_id = $2`, runID, endpointID))
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
    (id, automation_id, project_id, automation_name, endpoint_id, action_type,
     template_id, template_name, trigger_source, auth_mode, event_id, event_type,
     event_source, event_time, idempotency_hash, idempotency_fingerprint,
     payload_sha256, payload_bytes, status, error_code, error_message, received_at, expires_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
      $15, $16, $17, $18, $19, '', '', $20, $21)`,
		run.ID, run.AutomationID, run.ProjectID, run.AutomationName, run.EndpointID,
		run.ActionType, run.TemplateID, run.TemplateName, run.TriggerSource, run.AuthMode,
		run.Event.ID, run.Event.Type, run.Event.Source, run.Event.Time, nullableBytes(idempotencyHash),
		run.IdempotencyFingerprint, payloadHash, run.PayloadBytes, run.Status, run.ReceivedAt, run.ExpiresAt,
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
	if err := tx.Commit(ctx); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("commit finished automation run: %w", err)
	}
	run.Status = status
	run.ErrorCode = code
	run.ErrorMessage = message
	run.FinishedAt = &finishedAt
	return s.automationTriggerResult(run, false), nil
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
	automation platform.AutomationInput,
	automationID, runID string,
	templateResource platform.Resource,
	payload any,
	headers map[string]string,
	query map[string]any,
	event platform.AutomationEvent,
) (platform.Input, []byte, error) {
	projectID := automation.ProjectID
	shortID := strings.ReplaceAll(runID, "-", "")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	base := map[string]any{
		"id":          "auto-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"kind":        "sandbox",
		"projectId":   projectID,
		"name":        automation.Name + "-" + shortID,
		"description": "由自动化 " + automation.Name + " 创建",
		"enabled":     true,
		"spec":        cloneMap(templateResource.Spec),
	}
	base["spec"].(map[string]any)["modelBindings"] = automation.Action.ModelBindings
	context := automationTemplateContext(automation, automationID, runID, templateResource, payload, headers, query, event)
	patch, err := renderAutomationPatch(automation.Action.InputTemplate, context)
	if err != nil {
		return platform.Input{}, nil, err
	}
	merged := mergeAutomationPatch(base, patch)
	merged["id"] = base["id"]
	merged["kind"] = "sandbox"
	merged["projectId"] = projectID
	merged["enabled"] = true
	spec, ok := merged["spec"].(map[string]any)
	if !ok {
		return platform.Input{}, nil, errors.New("沙箱 Spec 必须是 JSON 对象")
	}
	spec["runtimeId"] = templateResource.ID
	spec["policy"] = "new"
	spec["status"] = "requested"
	spec["automationId"] = automationID
	spec["automationRunId"] = runID
	if automation.Action.ExpiresAfterSeconds > 0 {
		spec["expiresAt"] = event.ReceivedAt.Add(time.Duration(automation.Action.ExpiresAfterSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return platform.Input{}, nil, fmt.Errorf("编码沙箱输入失败: %w", err)
	}
	var input platform.Input
	if err := json.Unmarshal(encoded, &input); err != nil {
		return platform.Input{}, nil, fmt.Errorf("沙箱输入类型无效: %w", err)
	}
	platform.Normalize(&input)
	return input, encoded, nil
}

func automationTemplateContext(
	automation platform.AutomationInput,
	automationID, runID string,
	templateResource platform.Resource,
	payload any,
	headers map[string]string,
	query map[string]any,
	event platform.AutomationEvent,
) map[string]any {
	shortID := strings.ReplaceAll(runID, "-", "")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return map[string]any{
		"payload": payload,
		"headers": headers,
		"query":   query,
		"event": map[string]any{
			"id": event.ID, "type": event.Type, "source": event.Source,
			"time":       event.Time.Format(time.RFC3339Nano),
			"receivedAt": event.ReceivedAt.Format(time.RFC3339Nano),
		},
		"run":        map[string]any{"id": runID, "shortId": shortID},
		"automation": map[string]any{"id": automationID, "name": automation.Name},
		"project":    map[string]any{"id": automation.ProjectID},
		"template": map[string]any{
			"id": templateResource.ID, "name": templateResource.Name, "spec": templateResource.Spec,
		},
	}
}

func enqueueAutomationSandboxJob(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, runID string) (string, map[string]any, error) {
	payload, driver, imageReference, err := buildSandboxJobPayload(ctx, tx, sandbox)
	if err != nil {
		return "", nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources
		SET spec = spec || jsonb_build_object('driver', $1::text, 'imageReference', $2::text)
		WHERE id = $3 AND kind = 'sandbox'`, driver, imageReference, sandbox.ID); err != nil {
		return "", nil, fmt.Errorf("persist sandbox runtime snapshot: %w", err)
	}
	payload["driver"] = driver
	now := time.Now().UTC()
	jobID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
		(id, server_id, resource_id, action, status, payload, automation_run_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'create-sandbox', 'pending', $4::jsonb, $5, $6, $6)`,
		jobID, sandbox.Spec["serverId"], sandbox.ID, mustMapJSON(payload), runID, now); err != nil {
		return "", nil, fmt.Errorf("enqueue automated sandbox creation: %w", err)
	}
	return jobID, payload, nil
}

func enqueueAutomationDeleteJob(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, runID string) (string, error) {
	status, _ := sandbox.Spec["status"].(string)
	switch status {
	case "requested", "starting", "stopping", "restarting", "deleting":
		return "", errors.New("目标沙箱已有操作正在进行")
	}
	serverID, _ := sandbox.Spec["serverId"].(string)
	driver, _ := sandbox.Spec["driver"].(string)
	if driver == "" {
		runtimeID, _ := sandbox.Spec["runtimeId"].(string)
		if err := tx.QueryRow(ctx, `SELECT spec->>'driver' FROM control_resources
			WHERE id = $1 AND kind = 'runtime'`, runtimeID).Scan(&driver); err != nil {
			return "", fmt.Errorf("load sandbox runtime driver: %w", err)
		}
	}
	if serverID == "" || driver == "" {
		return "", errors.New("目标沙箱缺少服务器或隔离驱动信息")
	}
	jobID := uuid.NewString()
	now := time.Now().UTC()
	payload := map[string]any{
		"sandboxId": sandbox.ID, "externalId": sandbox.Spec["externalId"], "driver": driver,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
		(id, server_id, resource_id, action, status, payload, automation_run_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'delete-sandbox', 'pending', $4::jsonb, $5, $6, $6)`,
		jobID, serverID, sandbox.ID, mustMapJSON(payload), runID, now); err != nil {
		return "", fmt.Errorf("enqueue automated sandbox deletion: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET
		spec = spec || jsonb_build_object('status', 'deleting'::text, 'message', '自动化正在删除沙箱'::text),
		updated_at = $1 WHERE id = $2`, now, sandbox.ID); err != nil {
		return "", fmt.Errorf("mark automated sandbox deletion: %w", err)
	}
	return jobID, nil
}

func ensureAutomatedSandboxReferences(ctx context.Context, tx pgx.Tx, input platform.Input) error {
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
	serverID, _ := effectiveSpec["serverId"].(string)
	driver, _ := effectiveSpec["driver"].(string)
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
	for kind, key := range map[platform.Kind]string{
		platform.KindSkill: "skillIds", platform.KindMCP: "mcpServerIds", platform.KindVariable: "variableIds",
	} {
		for _, resourceID := range specStringList(effectiveSpec, key) {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM control_resources
          WHERE id = $1 AND kind = $2 AND enabled = TRUE)`, resourceID, kind).Scan(&exists); err != nil {
				return fmt.Errorf("check automated sandbox binding: %w", err)
			}
			if !exists {
				return &platform.ValidationError{Message: "沙箱包含不存在或已停用的能力配置"}
			}
		}
	}
	allowedCredentialIDs := specStringList(effectiveSpec, "credentialIds")
	modelBindings := specStringMap(effectiveSpec, "modelBindings")
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
		if err := tx.QueryRow(ctx, `SELECT protocol FROM provider_credentials
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
	if incompatible := incompatibleAgentTools(specStringList(effectiveSpec, "agentTools"), credentialProtocols); len(incompatible) > 0 {
		return &platform.ValidationError{Message: strings.Join(incompatible, "、") + " 与当前所选模型服务的接口协议不兼容"}
	}
	input.Spec["credentialIds"] = allowedCredentialIDs
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
		if err != nil || absDuration(time.Since(time.Unix(timestamp, 0))) > webhookSignatureWindow {
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
		if err != nil || absDuration(time.Since(time.Unix(timestamp, 0))) > webhookSignatureWindow {
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
		for _, signature := range strings.Fields(automationHeader(delivery.Headers, "webhook-signature")) {
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

func decodeAutomationPayload(body []byte) (any, error) {
	if len(body) == 0 {
		return nil, errors.New("Webhook 请求内容不能为空")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("Webhook 请求必须包含有效 JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Webhook 请求只能包含一个 JSON 值")
	}
	return payload, nil
}

func filteredAutomationHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized := strings.ToLower(strings.TrimSpace(key))
		switch normalized {
		case "authorization", "cookie", "proxy-authorization", "x-agentbox-signature",
			"x-hub-signature-256", "x-gitlab-token", "webhook-signature":
			continue
		}
		result[normalized] = value
	}
	return result
}

func scanAutomation(row pgx.Row) (platform.Automation, error) {
	var result platform.Automation
	var triggerType, actionType string
	var createdBy, updatedBy pgtype.Text
	var modelBindingsJSON []byte
	err := row.Scan(
		&result.ID, &result.ProjectID, &result.Name, &result.Description, &result.Enabled,
		&triggerType, &actionType, &result.Trigger.AuthMode, &result.EndpointID, &result.SecretLastFour,
		&result.Action.TemplateID, &modelBindingsJSON, &result.Action.InputTemplate,
		&result.ConditionTemplate, &result.Action.TargetTemplate, &result.Action.CommandTemplate,
		&result.Action.TimeoutSeconds, &result.Action.CleanupPolicy, &result.Action.ExpiresAfterSeconds,
		&createdBy, &updatedBy,
		&result.LastTriggeredAt, &result.SecretRotatedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	result.Trigger.Type = triggerType
	result.Action.Type = actionType
	if err == nil {
		err = json.Unmarshal(modelBindingsJSON, &result.Action.ModelBindings)
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
	var triggerType, actionType string
	var createdBy, updatedBy pgtype.Text
	var modelBindingsJSON []byte
	err := row.Scan(
		&result.Automation.ID, &result.Automation.ProjectID, &result.Automation.Name,
		&result.Automation.Description, &result.Automation.Enabled, &triggerType, &actionType,
		&result.Automation.Trigger.AuthMode, &result.Automation.EndpointID,
		&result.Automation.SecretLastFour, &result.Automation.Action.TemplateID,
		&modelBindingsJSON, &result.Automation.Action.InputTemplate,
		&result.Automation.ConditionTemplate, &result.Automation.Action.TargetTemplate,
		&result.Automation.Action.CommandTemplate, &result.Automation.Action.TimeoutSeconds,
		&result.Automation.Action.CleanupPolicy, &result.Automation.Action.ExpiresAfterSeconds,
		&createdBy, &updatedBy,
		&result.Automation.LastTriggeredAt, &result.Automation.SecretRotatedAt,
		&result.Automation.CreatedAt, &result.Automation.UpdatedAt,
		&result.SecretHash, &result.SecretCiphertext, &result.SecretNonce,
	)
	result.Automation.Trigger.Type = triggerType
	result.Automation.Action.Type = actionType
	if err == nil {
		err = json.Unmarshal(modelBindingsJSON, &result.Automation.Action.ModelBindings)
	}
	if createdBy.Valid {
		result.Automation.CreatedBy = &createdBy.String
	}
	if updatedBy.Valid {
		result.Automation.UpdatedBy = &updatedBy.String
	}
	return result, err
}

func scanAutomationRun(row pgx.Row) (platform.AutomationRun, error) {
	var result platform.AutomationRun
	var automationID, sandboxID, workerJobID pgtype.Text
	var eventTime pgtype.Timestamptz
	var exitCode pgtype.Int4
	err := row.Scan(
		&result.ID, &automationID, &result.ProjectID, &result.AutomationName,
		&result.EndpointID, &result.ActionType, &result.TemplateID, &result.TemplateName,
		&result.TriggerSource, &result.AuthMode, &result.Event.ID, &result.Event.Type,
		&result.Event.Source, &eventTime,
		&result.IdempotencyFingerprint, &result.PayloadSHA256, &result.PayloadBytes,
		&result.InputSHA256, &result.Status, &sandboxID, &workerJobID, &exitCode,
		&result.Output, &result.OutputTruncated, &result.CleanupStatus, &result.ErrorCode,
		&result.ErrorMessage, &result.ReceivedAt, &result.QueuedAt, &result.StartedAt,
		&result.FinishedAt, &result.ExpiresAt,
	)
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
	if exitCode.Valid {
		value := int(exitCode.Int32)
		result.ExitCode = &value
	}
	return result, err
}

func automationInputFromStored(automation platform.Automation) platform.AutomationInput {
	return platform.AutomationInput{
		ProjectID: automation.ProjectID, Name: automation.Name, Description: automation.Description,
		Enabled: automation.Enabled, ConditionTemplate: automation.ConditionTemplate,
		Trigger: automation.Trigger, Action: automation.Action,
	}
}

func validateAutomationInputTemplates(input platform.AutomationInput) error {
	if err := validateAutomationStringTemplate("执行条件", input.ConditionTemplate); err != nil {
		return err
	}
	switch input.Action.Type {
	case "create-sandbox":
		return validateAutomationTemplate(input.Action.InputTemplate)
	case "run-task":
		if err := validateAutomationTemplate(input.Action.InputTemplate); err != nil {
			return err
		}
		return validateAutomationStringTemplate("任务命令模板", input.Action.CommandTemplate)
	case "destroy-sandbox":
		return validateAutomationStringTemplate("目标模板", input.Action.TargetTemplate)
	default:
		return nil
	}
}

func automationModelBindingsJSON(value map[string]string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func newWebhookSecret() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	return "abx_wh_" + token, nil
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func stringPointer(value string) *string { return &value }
