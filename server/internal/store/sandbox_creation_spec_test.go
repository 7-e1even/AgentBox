package store

import (
	"testing"

	"agentbox/internal/platform"
)

func TestPersistSandboxCreationSpecUsesDriverNetworkDefault(t *testing.T) {
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
	var network, proxyID, credentialedProxyID string
	var hasCredentialedProxyMarker bool
	if err := s.pool.QueryRow(ctx, `SELECT spec->>'network', spec->>'proxyId',
	  spec->>'credentialedProxyIdAtCreation', spec ? 'credentialedProxyIdAtCreation'
	  FROM control_resources WHERE id = $1`, sandboxID).Scan(
		&network, &proxyID, &credentialedProxyID, &hasCredentialedProxyMarker,
	); err != nil {
		t.Fatalf("load creation snapshot: %v", err)
	}
	if network != "none" {
		t.Fatalf("snapshot network = %q, want none", network)
	}
	if proxyID != "" || credentialedProxyID != "" || !hasCredentialedProxyMarker {
		t.Fatalf("proxy provenance = %q/%q present=%v, want explicit empty snapshot", proxyID, credentialedProxyID, hasCredentialedProxyMarker)
	}
}

func TestSandboxCredentialedProxyProvenanceRequiresExactServerMarker(t *testing.T) {
	for _, test := range []struct {
		name string
		spec map[string]any
		want bool
	}{
		{name: "matching", spec: map[string]any{sandboxCredentialedProxyAtCreationField: "proxy-one"}, want: true},
		{name: "absent", spec: map[string]any{}},
		{name: "empty", spec: map[string]any{sandboxCredentialedProxyAtCreationField: ""}},
		{name: "different", spec: map[string]any{sandboxCredentialedProxyAtCreationField: "proxy-two"}},
		{name: "wrong type", spec: map[string]any{sandboxCredentialedProxyAtCreationField: true}},
		{name: "not normalized", spec: map[string]any{sandboxCredentialedProxyAtCreationField: " proxy-one "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sandboxCredentialedProxyProvenanceMatches(test.spec, "proxy-one"); got != test.want {
				t.Fatalf("sandboxCredentialedProxyProvenanceMatches() = %v, want %v", got, test.want)
			}
		})
	}
}
