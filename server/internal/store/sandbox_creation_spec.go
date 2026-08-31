package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"agentbox/internal/platform"
	"github.com/jackc/pgx/v5"
)

// These fields configure the instance at creation, not on start/restart.
var sandboxCreationFields = []string{
	"runtimeId", "serverId", "driver", "imageReference", "imageId", "cpu", "memory",
	"desktop", "network", "workdir", "setup", "workspace",
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
		if !reflect.DeepEqual(current[key], input[key]) {
			return &platform.ValidationError{Message: fmt.Sprintf("沙箱创建后不能修改 %s；请创建新的沙箱", key)}
		}
	}
	return nil
}

// Freeze the effective creation settings for both manual and automated
// sandboxes. Later template edits must not change an existing instance's shape.
func persistSandboxCreationSpec(ctx context.Context, tx pgx.Tx, sandbox platform.Resource, payload map[string]any, driver, imageReference string) error {
	snapshot := map[string]any{
		"driver": driver, "imageReference": imageReference,
		"cpu": "", "memory": "", "desktop": false, "network": "restricted",
		"workdir": "/workspace", "setup": "", "workspace": "",
	}
	for key := range snapshot {
		if value := payload[key]; value != nil {
			snapshot[key] = value
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET spec = spec || $1::jsonb
    WHERE id = $2 AND kind = 'sandbox'`, mustMapJSON(snapshot), sandbox.ID); err != nil {
		return fmt.Errorf("persist sandbox creation configuration: %w", err)
	}
	for key, value := range snapshot {
		sandbox.Spec[key] = value
	}
	return nil
}
