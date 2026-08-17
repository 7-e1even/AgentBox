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
	ErrAutomationDisabled  = errors.New("automation disabled")
	ErrWebhookUnauthorized = errors.New("webhook unauthorized")
	ErrAutomationRateLimit = errors.New("automation rate limit")
)

const (
	automationRequestsPerMinute = 30
	automationInflightLimit     = 5
	webhookSignatureWindow      = 5 * time.Minute
)

const automationColumns = `id::text, project_id, name, description, enabled,
  trigger_type, action_type, auth_mode, endpoint_id::text, secret_last_four,
  template_id, model_bindings, input_template, created_by::text, updated_by::text,
  last_triggered_at, secret_rotated_at, created_at, updated_at`

const automationRunColumns = `id::text, automation_id::text, project_id, automation_name,
  template_id, template_name, trigger_source, auth_mode, idempotency_fingerprint,
  encode(payload_sha256, 'hex'), payload_bytes, COALESCE(encode(input_sha256, 'hex'), ''),
  status, sandbox_id, worker_job_id::text, error_code, error_message,
  received_at, queued_at, started_at, finished_at`

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
	if err := validateAutomationTemplate(input.Action.InputTemplate); err != nil {
		return platform.Automation{}, "", &platform.ValidationError{Message: err.Error()}
	}
	if _, err := loadAutomationTemplate(ctx, s.pool, input.ProjectID, input.Action.TemplateID); err != nil {
		return platform.Automation{}, "", err
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return platform.Automation{}, "", err
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, secret)
	if err != nil {
		return platform.Automation{}, "", err
	}
	now := time.Now().UTC()
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `INSERT INTO automations
    (id, project_id, name, description, enabled, trigger_type, action_type, auth_mode,
     endpoint_id, secret_hash, secret_ciphertext, secret_nonce, secret_last_four,
     template_id, model_bindings, input_template, created_by, updated_by, secret_rotated_at, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, 'webhook', 'create-sandbox', $6, $7, $8, $9, $10,
      $11, $12, $13, $14, $15, $15, $16, $16, $16)
    RETURNING `+automationColumns,
		uuid.NewString(), input.ProjectID, input.Name, input.Description, input.Enabled,
		input.Trigger.AuthMode, uuid.NewString(), hashToken(secret), ciphertext, nonce,
		lastFour(secret), input.Action.TemplateID, automationModelBindingsJSON(input.Action.ModelBindings),
		input.Action.InputTemplate, userID, now,
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
	if err := validateAutomationTemplate(input.Action.InputTemplate); err != nil {
		return platform.Automation{}, &platform.ValidationError{Message: err.Error()}
	}
	if _, err := loadAutomationTemplate(ctx, s.pool, input.ProjectID, input.Action.TemplateID); err != nil {
		return platform.Automation{}, err
	}
	automation, err := scanAutomation(s.pool.QueryRow(ctx, `UPDATE automations SET
    project_id = $1, name = $2, description = $3, enabled = $4, auth_mode = $5,
    template_id = $6, model_bindings = $7, input_template = $8, updated_by = $9, updated_at = $10
    WHERE id = $11 RETURNING `+automationColumns,
		input.ProjectID, input.Name, input.Description, input.Enabled, input.Trigger.AuthMode,
		input.Action.TemplateID, automationModelBindingsJSON(input.Action.ModelBindings),
		input.Action.InputTemplate, userID, time.Now().UTC(), id,
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

func (s *Store) PreviewAutomation(ctx context.Context, input platform.AutomationPreviewInput) (platform.ResourceInputPreview, error) {
	platform.NormalizeAutomationInput(&input.Automation)
	if err := platform.ValidateAutomationInput(input.Automation); err != nil {
		return platform.ResourceInputPreview{}, err
	}
	templateResource, err := loadAutomationTemplate(ctx, s.pool, input.Automation.ProjectID, input.Automation.Action.TemplateID)
	if err != nil {
		return platform.ResourceInputPreview{}, err
	}
	runID := uuid.NewString()
	sandboxInput, encoded, err := buildAutomatedSandboxInput(
		input.Automation, "preview", runID, templateResource, input.Payload,
		filteredAutomationHeaders(input.Headers), input.Query, time.Now().UTC(),
	)
	if err != nil {
		return platform.ResourceInputPreview{}, &platform.ValidationError{Message: err.Error()}
	}
	_ = encoded
	if err := platform.Validate(sandboxInput); err != nil {
		return platform.ResourceInputPreview{}, err
	}
	if err := s.ensureResourceReferences(ctx, sandboxInput, true); err != nil {
		return platform.ResourceInputPreview{}, err
	}
	return platform.ResourceInputPreview{
		ID: sandboxInput.ID, Kind: sandboxInput.Kind, ProjectID: sandboxInput.ProjectID,
		Name: sandboxInput.Name, Description: sandboxInput.Description,
		Enabled: sandboxInput.Enabled, Spec: sandboxInput.Spec,
	}, nil
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

	idempotencyHash, fingerprint := automationIdempotency(delivery, stored.Automation.Trigger.AuthMode)
	if len(idempotencyHash) > 0 {
		existing, err := scanAutomationRun(tx.QueryRow(ctx, `SELECT `+automationRunColumns+`
      FROM automation_runs WHERE automation_id = $1 AND idempotency_hash = $2`, stored.Automation.ID, idempotencyHash))
		if err == nil {
			return platform.AutomationTriggerResult{Run: existing, Duplicate: true}, nil
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

	receivedAt := time.Now().UTC()
	runID := uuid.NewString()
	payloadHash := sha256.Sum256(delivery.Body)
	templateResource, templateErr := loadAutomationTemplate(ctx, tx, stored.Automation.ProjectID, stored.Automation.Action.TemplateID)
	templateName := stored.Automation.Action.TemplateID
	if templateErr == nil {
		templateName = templateResource.Name
	}
	run := platform.AutomationRun{
		ID: runID, AutomationID: stringPointer(stored.Automation.ID),
		ProjectID: stored.Automation.ProjectID, AutomationName: stored.Automation.Name,
		TemplateID: stored.Automation.Action.TemplateID, TemplateName: templateName,
		TriggerSource: triggerSource, AuthMode: stored.Automation.Trigger.AuthMode,
		IdempotencyFingerprint: fingerprint, PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		PayloadBytes: len(delivery.Body), Status: platform.AutomationRunEvaluating,
		ReceivedAt: receivedAt,
	}
	if err := insertAutomationRun(ctx, tx, run, idempotencyHash, payloadHash[:]); err != nil {
		return platform.AutomationTriggerResult{}, err
	}

	if templateErr != nil {
		if !platform.IsValidationError(templateErr) {
			return platform.AutomationTriggerResult{}, templateErr
		}
		return finishFailedAutomationRun(ctx, tx, stored.Automation.ID, run, "template_invalid", templateErr.Error(), receivedAt)
	}
	sandboxInput, encodedInput, buildErr := buildAutomatedSandboxInput(
		automationInputFromStored(stored.Automation), stored.Automation.ID, runID,
		templateResource, payload, filteredAutomationHeaders(delivery.Headers), delivery.Query, receivedAt,
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
		return finishFailedAutomationRun(ctx, tx, stored.Automation.ID, run, "input_invalid", buildErr.Error(), receivedAt)
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
	jobID, err := enqueueSandboxJob(ctx, tx, resource)
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
	if _, err := tx.Exec(ctx, `UPDATE automations SET last_triggered_at = $1 WHERE id = $2`, receivedAt, stored.Automation.ID); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("update automation trigger time: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("commit automation trigger: %w", err)
	}
	run.Status = platform.AutomationRunQueued
	run.SandboxID = stringPointer(resource.ID)
	run.WorkerJobID = stringPointer(jobID)
	run.InputSHA256 = hex.EncodeToString(inputHash[:])
	run.QueuedAt = &queuedAt
	return platform.AutomationTriggerResult{Run: run}, nil
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

func insertAutomationRun(ctx context.Context, tx pgx.Tx, run platform.AutomationRun, idempotencyHash, payloadHash []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO automation_runs
    (id, automation_id, project_id, automation_name, template_id, template_name,
     trigger_source, auth_mode, idempotency_hash, idempotency_fingerprint,
     payload_sha256, payload_bytes, status, error_code, error_message, received_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, '', '', $14)`,
		run.ID, run.AutomationID, run.ProjectID, run.AutomationName, run.TemplateID,
		run.TemplateName, run.TriggerSource, run.AuthMode, nullableBytes(idempotencyHash),
		run.IdempotencyFingerprint, payloadHash, run.PayloadBytes, run.Status, run.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("insert automation run: %w", err)
	}
	return nil
}

func finishFailedAutomationRun(
	ctx context.Context,
	tx pgx.Tx,
	automationID string,
	run platform.AutomationRun,
	code, message string,
	triggeredAt time.Time,
) (platform.AutomationTriggerResult, error) {
	finishedAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'failed', error_code = $1,
      error_message = $2, finished_at = $3 WHERE id = $4`, code, message, finishedAt, run.ID); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("fail automation run: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE automations SET last_triggered_at = $1 WHERE id = $2`, triggeredAt, automationID); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("update failed automation trigger time: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.AutomationTriggerResult{}, fmt.Errorf("commit failed automation run: %w", err)
	}
	run.Status = platform.AutomationRunFailed
	run.ErrorCode = code
	run.ErrorMessage = message
	run.FinishedAt = &finishedAt
	return platform.AutomationTriggerResult{Run: run}, nil
}

func buildAutomatedSandboxInput(
	automation platform.AutomationInput,
	automationID, runID string,
	templateResource platform.Resource,
	payload any,
	headers map[string]string,
	query map[string]any,
	receivedAt time.Time,
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
	context := map[string]any{
		"payload": payload,
		"headers": headers,
		"query":   query,
		"event": map[string]any{
			"receivedAt": receivedAt.Format(time.RFC3339Nano),
		},
		"run": map[string]any{"id": runID, "shortId": shortID},
		"automation": map[string]any{
			"id": automationID, "name": automation.Name,
		},
		"project": map[string]any{"id": projectID},
		"template": map[string]any{
			"id": templateResource.ID, "name": templateResource.Name, "spec": templateResource.Spec,
		},
	}
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
	default:
		return ErrWebhookUnauthorized
	}
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

func automationIdempotency(delivery platform.AutomationDelivery, mode platform.AutomationAuthMode) ([]byte, string) {
	value := strings.TrimSpace(delivery.IdempotencyKey)
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
		case "authorization", "cookie", "proxy-authorization", "x-agentbox-signature":
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
		&result.Action.TemplateID, &modelBindingsJSON, &result.Action.InputTemplate, &createdBy, &updatedBy,
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
		&modelBindingsJSON, &result.Automation.Action.InputTemplate, &createdBy, &updatedBy,
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
	err := row.Scan(
		&result.ID, &automationID, &result.ProjectID, &result.AutomationName,
		&result.TemplateID, &result.TemplateName, &result.TriggerSource, &result.AuthMode,
		&result.IdempotencyFingerprint, &result.PayloadSHA256, &result.PayloadBytes,
		&result.InputSHA256, &result.Status, &sandboxID, &workerJobID, &result.ErrorCode,
		&result.ErrorMessage, &result.ReceivedAt, &result.QueuedAt, &result.StartedAt, &result.FinishedAt,
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
	return result, err
}

func automationInputFromStored(automation platform.Automation) platform.AutomationInput {
	return platform.AutomationInput{
		ProjectID: automation.ProjectID, Name: automation.Name, Description: automation.Description,
		Enabled: automation.Enabled, Trigger: automation.Trigger, Action: automation.Action,
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
