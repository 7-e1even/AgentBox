package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"agentbox/internal/platform"
	"github.com/jackc/pgx/v5"
)

func loadSandboxExtensions(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, spec map[string]any, creating bool) ([]platform.ExtensionDefinition, error) {
	definitions := []platform.ExtensionDefinition{}
	if !creating {
		// An older instance without a snapshot never inherits later template extensions.
		if snapshot := sandbox.Spec["extensionSnapshots"]; snapshot != nil {
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				return nil, fmt.Errorf("encode extension snapshot: %w", err)
			}
			if err := json.Unmarshal(encoded, &definitions); err != nil {
				return nil, fmt.Errorf("decode extension snapshot: %w", err)
			}
		}
		return definitions, nil
	}
	for _, id := range specStringList(spec, "extensionIds") {
		var definition platform.ExtensionDefinition
		var encoded []byte
		err := tx.QueryRow(ctx, `SELECT id, name, description, generation, spec
      FROM control_resources WHERE id = $1 AND kind = 'extension' AND enabled = TRUE
        AND project_id = $2`, id, sandbox.ProjectID).Scan(
			&definition.ID, &definition.Name, &definition.Description, &definition.Generation, &encoded,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &platform.ValidationError{Message: "扩展不存在、已停用或不属于当前项目: " + id}
		}
		if err != nil {
			return nil, fmt.Errorf("load extension definition: %w", err)
		}
		if err := json.Unmarshal(encoded, &definition.Spec); err != nil {
			return nil, fmt.Errorf("decode extension definition: %w", err)
		}
		if err := platform.ValidateExtensionSpec(definition.Spec, true); err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func ensureExtensionReferences(ctx context.Context, tx pgx.Tx, projectID *string, spec map[string]any, creating bool) error {
	definitions, err := loadSandboxExtensions(ctx, tx, platform.Resource{Input: platform.Input{ProjectID: projectID}}, spec, true)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.Spec.RequiresNetwork && spec["network"] == "none" {
			return &platform.ValidationError{Message: "完全隔离的环境不能安装需要网络的扩展: " + definition.Name}
		}
	}
	if creating && len(definitions) > 0 {
		var supported bool
		if err := tx.QueryRow(ctx, `SELECT capabilities ? 'sandbox-extensions' FROM managed_servers
      WHERE id = $1`, spec["serverId"]).Scan(&supported); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &platform.ValidationError{Message: "目标服务器不存在"}
			}
			return fmt.Errorf("check extension Worker capability: %w", err)
		}
		if !supported {
			return &platform.ValidationError{Message: "目标 Worker 尚不支持沙箱扩展，请先更新 Worker"}
		}
	}
	return nil
}

func extensionIDs(definitions []platform.ExtensionDefinition) []string {
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	return ids
}

func validateExtensionProgressPayload(input platform.WorkerJobProgressInput, action string, payloadJSON []byte) error {
	if input.ExtensionID == "" {
		return nil
	}
	var payload struct {
		Extensions []platform.ExtensionDefinition `json:"extensions"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return fmt.Errorf("decode extension progress job: %w", err)
	}
	if action != "create-sandbox" || !slices.Contains(extensionIDs(payload.Extensions), input.ExtensionID) {
		return &platform.ValidationError{Message: "该任务未配置此扩展"}
	}
	return nil
}

func unverifiedSandboxExtension(payloadJSON, progressJSON []byte) (string, error) {
	var payload struct {
		Extensions []struct {
			ID string `json:"id"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", fmt.Errorf("decode completed extension definitions: %w", err)
	}
	var progress platform.ProvisioningProgress
	if err := json.Unmarshal(progressJSON, &progress); err != nil {
		return "", fmt.Errorf("decode completed extension progress: %w", err)
	}
	for _, expected := range payload.Extensions {
		index := slices.IndexFunc(progress.Extensions, func(extension platform.ProvisioningExtension) bool {
			return extension.ID == expected.ID
		})
		if index == -1 || progress.Extensions[index].Status != "succeeded" {
			return expected.ID, nil
		}
	}
	return "", nil
}

func advanceExtensionProgress(current *platform.ProvisioningProgress, input platform.WorkerJobProgressInput, now time.Time) {
	if input.ExtensionID == "" {
		return
	}
	index := slices.IndexFunc(current.Extensions, func(extension platform.ProvisioningExtension) bool {
		return extension.ID == input.ExtensionID
	})
	if index == -1 {
		current.Extensions = append(current.Extensions, platform.ProvisioningExtension{ID: input.ExtensionID, StartedAt: &now})
		index = len(current.Extensions) - 1
	}
	extension := &current.Extensions[index]
	extension.Status = input.ExtensionStatus
	extension.Message = input.Message
	if input.ExtensionOutput != "" {
		extension.Output = input.ExtensionOutput
	}
	if extension.StartedAt == nil {
		extension.StartedAt = &now
	}
	extension.DurationMS = now.Sub(*extension.StartedAt).Milliseconds()
	if input.ExtensionStatus == "succeeded" || input.ExtensionStatus == "failed" {
		extension.FinishedAt = &now
	} else {
		extension.FinishedAt = nil
	}
}

func finishExtensionProgress(current *platform.ProvisioningProgress, result platform.WorkerJobResult, now time.Time) {
	for index := range current.Extensions {
		extension := &current.Extensions[index]
		if extension.FinishedAt != nil {
			continue
		}
		extension.FinishedAt = &now
		if extension.StartedAt != nil {
			extension.DurationMS = now.Sub(*extension.StartedAt).Milliseconds()
		}
		// Only an explicit successful verification makes an extension installed.
		extension.Status = "failed"
		if installationCancelled(result) {
			extension.Status = "cancelled"
		}
		extension.Message = result.Message
	}
}
