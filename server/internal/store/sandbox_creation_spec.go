package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"agentbox/internal/platform"
	"github.com/jackc/pgx/v5"
)

const sandboxCredentialedProxyAtCreationField = "credentialedProxyIdAtCreation"

// These fields configure the instance at creation, not on start/restart.
var sandboxCreationFields = []string{
	"runtimeId", "serverId", "driver", "imageReference", "imageId", "cpu", "memory",
	"desktop", "network", "workdir", "setup", "workspace",
	"extensionIds",
}

func validateSandboxCreationSpec(ctx context.Context, tx pgx.Tx, id string, input map[string]any) error {
	var encoded []byte
	if err := tx.QueryRow(ctx, `SELECT spec FROM control_resources
    WHERE id = $1 AND kind = 'sandbox' FOR UPDATE`, id).Scan(&encoded); errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	} else if err != nil {
		return fmt.Errorf("load sandbox creation configuration: %w", err)
	}
	var current map[string]any
	if err := json.Unmarshal(encoded, &current); err != nil {
		return fmt.Errorf("decode sandbox creation configuration: %w", err)
	}
	for _, key := range sandboxCreationFields {
		if key == "extensionIds" {
			if !slices.Equal(specStringList(current, key), specStringList(input, key)) {
				return &platform.ValidationError{Message: "沙箱创建后不能修改扩展；请创建新的沙箱"}
			}
			if _, exists := current[key]; !exists {
				delete(input, key)
			}
			continue
		}
		if !reflect.DeepEqual(current[key], input[key]) {
			return &platform.ValidationError{Message: fmt.Sprintf("沙箱创建后不能修改 %s；请创建新的沙箱", key)}
		}
	}
	return nil
}

// Freeze the effective creation settings for both manual and automated
// sandboxes. Later template edits must not change an existing instance's shape.
func persistSandboxCreationSpec(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, payload map[string]any, driver, imageReference string) error {
	proxyID, _ := payload["proxyId"].(string)
	proxyID = strings.TrimSpace(proxyID)
	credentialedProxyID, err := credentialedProxyCreationMarker(ctx, tx, proxyID)
	if err != nil {
		return err
	}
	snapshot := map[string]any{
		"driver": driver, "imageReference": imageReference,
		"cpu": "", "memory": "", "desktop": false,
		"network": platform.EffectiveNetworkPolicy(driver, ""),
		"workdir": "/workspace", "setup": "", "workspace": "",
		"extensionIds": []string{}, "extensionSnapshots": []platform.ExtensionDefinition{},
	}
	for key := range snapshot {
		if value := payload[key]; value != nil {
			snapshot[key] = value
		}
	}
	if definitions := payload["extensions"]; definitions != nil {
		snapshot["extensionSnapshots"] = definitions
	}
	// These two values are derived by the server from the effective creation
	// payload and current proxy record; payload metadata can never override them.
	snapshot["proxyId"] = proxyID
	snapshot[sandboxCredentialedProxyAtCreationField] = credentialedProxyID
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET spec = spec || $1::jsonb
    WHERE id = $2 AND kind = 'sandbox'`, mustMapJSON(snapshot), sandbox.ID); err != nil {
		return fmt.Errorf("persist sandbox creation configuration: %w", err)
	}
	for key, value := range snapshot {
		sandbox.Spec[key] = value
	}
	return nil
}

func credentialedProxyCreationMarker(ctx context.Context, query networkProxyQueryRow, proxyID string) (string, error) {
	if proxyID == "" {
		return "", nil
	}
	var hasPassword bool
	if err := query.QueryRow(ctx, `SELECT password_last_four <> ''
    FROM network_proxies WHERE id = $1`, proxyID).Scan(&hasPassword); errors.Is(err, pgx.ErrNoRows) {
		return "", &platform.ValidationError{Message: "沙箱包含不存在或已停用的网络代理"}
	} else if err != nil {
		return "", fmt.Errorf("snapshot sandbox proxy provenance: %w", err)
	}
	if hasPassword {
		return proxyID, nil
	}
	return "", nil
}

func validateSandboxProxyCreationProvenance(
	ctx context.Context,
	query networkProxyQueryRow,
	sandboxSpec map[string]any,
	proxyID string,
) error {
	proxyID = strings.TrimSpace(proxyID)
	if proxyID == "" {
		return nil
	}
	var hasPassword bool
	if err := query.QueryRow(ctx, `SELECT password_last_four <> ''
    FROM network_proxies WHERE id = $1`, proxyID).Scan(&hasPassword); errors.Is(err, pgx.ErrNoRows) {
		return &platform.ValidationError{Message: "沙箱包含不存在或已停用的网络代理"}
	} else if err != nil {
		return fmt.Errorf("check sandbox proxy provenance: %w", err)
	}
	if !hasPassword {
		return nil
	}
	if !sandboxCredentialedProxyProvenanceMatches(sandboxSpec, proxyID) {
		return fmt.Errorf("%w: 沙箱缺少当前带密码代理的可信创建记录；请使用该代理创建新沙箱", ErrConflict)
	}
	return nil
}

func sandboxCredentialedProxyProvenanceMatches(spec map[string]any, proxyID string) bool {
	credentialedProxyID, present := spec[sandboxCredentialedProxyAtCreationField].(string)
	return present && credentialedProxyID == proxyID
}

func validatePersistedSandboxProxyCreationProvenance(
	ctx context.Context,
	query networkProxyQueryRow,
	sandboxID, proxyID string,
) error {
	var encoded []byte
	if err := query.QueryRow(ctx, `SELECT spec FROM control_resources
    WHERE id = $1 AND kind = 'sandbox'`, sandboxID).Scan(&encoded); errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	} else if err != nil {
		return fmt.Errorf("load sandbox proxy provenance: %w", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return fmt.Errorf("decode sandbox proxy provenance: %w", err)
	}
	return validateSandboxProxyCreationProvenance(ctx, query, spec, proxyID)
}

func failUnsafeSandboxProxyJob(ctx context.Context, tx pgx.Tx, jobID, sandboxID, action string) error {
	const message = "沙箱缺少当前带密码代理的可信创建记录；请使用该代理创建新沙箱"
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET status = 'failed', lease_until = NULL,
    result_error_code = 'sandbox_proxy_provenance_invalid', result_error_stage = 'dispatch',
    result_error_retryable = FALSE, result_message = $1, updated_at = NOW()
    WHERE id = $2`, message, jobID); err != nil {
		return fmt.Errorf("retire unsafe sandbox proxy job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'failed',
    error_code = 'sandbox_proxy_provenance_invalid', error_retryable = FALSE,
    error_message = $1, finished_at = NOW()
    WHERE worker_job_id = $2 AND status IN ('queued', 'provisioning')`, message, jobID); err != nil {
		return fmt.Errorf("retire unsafe sandbox proxy automation run: %w", err)
	}
	var update string
	switch action {
	case "start-sandbox", "restart-sandbox":
		update = `spec || jsonb_build_object('status', 'error'::text, 'message', $1::text)`
	case "check-sandbox-agent-tools", "update-sandbox-agent-tools":
		update = `spec || jsonb_build_object('agentToolOperation',
      COALESCE(spec->'agentToolOperation', '{}'::jsonb) || jsonb_build_object(
        'status', 'failed'::text, 'message', $1::text,
        'updatedAt', NOW(), 'finishedAt', NOW()))`
	case "configure-sandbox-proxy":
		update = `spec || jsonb_build_object('proxyOperation',
      COALESCE(spec->'proxyOperation', '{}'::jsonb) || jsonb_build_object(
        'status', 'failed'::text, 'message', $1::text,
        'updatedAt', NOW(), 'finishedAt', NOW()))`
	default:
		update = `spec || jsonb_build_object('message', $1::text)`
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET spec = `+update+`, updated_at = NOW()
    WHERE id = $2 AND kind = 'sandbox'`, message, sandboxID); err != nil {
		return fmt.Errorf("mark unsafe sandbox proxy job failed: %w", err)
	}
	return nil
}
