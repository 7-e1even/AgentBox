package store

import (
	"testing"

	"agentbox/internal/platform"
)

func TestPersistSandboxCreationSpecDefaultsNetworkToEgress(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	const sandboxID = "snapshot-network-default"
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'sandbox', 'Snapshot test', '', TRUE, '{}'::jsonb, NOW(), NOW())`, sandboxID); err != nil {
		t.Fatalf("insert sandbox: %v", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin snapshot transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	sandbox := platform.Resource{Input: platform.Input{
		ID: sandboxID, Kind: platform.KindSandbox, Spec: map[string]any{},
	}}
	if err := persistSandboxCreationSpec(ctx, tx, sandbox, map[string]any{}, "docker", "ubuntu:24.04"); err != nil {
		t.Fatalf("persist creation snapshot: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit creation snapshot: %v", err)
	}
	var network string
	if err := s.pool.QueryRow(ctx, `SELECT spec->>'network' FROM control_resources WHERE id = $1`, sandboxID).Scan(&network); err != nil {
		t.Fatalf("load creation snapshot: %v", err)
	}
	if network != "egress" {
		t.Fatalf("snapshot network = %q, want egress", network)
	}
}
