package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestSkillCRUDSummaryAndReferencedLifecycle(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	projectID := "default"
	skillID := "skill-" + uuid.NewString()
	document := fmt.Sprintf("---\nname: %s\ndescription: Canonical description\nlicense: MIT\n---\n\nRun the review.\n", skillID)
	skill, err := s.CreateResource(ctx, platform.Input{
		ID: skillID, Kind: platform.KindSkill, ProjectID: &projectID,
		Name: "Lifecycle skill", Description: "client description", Enabled: true,
		Spec: map[string]any{
			"instructions": document,
			"bundleDigest": "client-controlled",
			"files": []any{map[string]any{
				"path": "references/guide.md", "content": base64.StdEncoding.EncodeToString([]byte("guide")),
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if skill.Description != "Canonical description" {
		t.Fatalf("description = %q", skill.Description)
	}
	digest, _ := skill.Spec["bundleDigest"].(string)
	if !strings.HasPrefix(digest, "sha256:") || digest == "client-controlled" {
		t.Fatalf("digest = %q", digest)
	}

	resources, err := s.ListResourcesFiltered(ctx, platform.ResourceFilter{Kind: platform.KindSkill, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	var summary platform.Resource
	for _, resource := range resources {
		if resource.ID == skillID {
			summary = resource
			break
		}
	}
	if summary.ID == "" || summary.Spec["bundleDigest"] != digest || summary.Spec["fileCount"] != 2 || summary.Spec["decodedBytes"] == nil {
		t.Fatalf("skill summary = %#v", summary.Spec)
	}
	if _, exists := summary.Spec["instructions"]; exists {
		t.Fatal("skill list returned instructions")
	}
	if _, exists := summary.Spec["files"]; exists {
		t.Fatal("skill list returned bundle files")
	}
	detail, err := s.GetResource(ctx, skillID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Spec["instructions"] == nil || detail.Spec["files"] == nil {
		t.Fatalf("skill detail was summarized: %#v", detail.Spec)
	}

	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatal(err)
	}
	inventory := platform.ServerInventory{DockerImages: []platform.ServerImage{{Reference: "ubuntu:24.04", Architecture: "amd64"}}}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, []string{"docker", workerFailClosedJobOutputCapability}, &inventory, "test"); err != nil {
		t.Fatal(err)
	}
	runtimeID := "runtime-" + uuid.NewString()
	runtime, err := s.CreateResource(ctx, platform.Input{
		ID: runtimeID, Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "Capability runtime", Enabled: true,
		Spec: map[string]any{
			"serverId": serverID, "driver": "docker", "imageReference": "ubuntu:24.04",
			"skillIds": []string{skillID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	frozenSandbox, err := s.CreateResource(ctx, platform.Input{
		ID: "sandbox-frozen-" + uuid.NewString(), Kind: platform.KindSandbox, ProjectID: &projectID,
		Name: "Frozen capabilities", Enabled: true,
		Spec: map[string]any{"serverId": serverID, "runtimeId": runtimeID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := specStringList(frozenSandbox.Spec, "skillIds"); len(got) != 1 || got[0] != skillID {
		t.Fatalf("new sandbox did not freeze Runtime capabilities: %#v", got)
	}
	for id, spec := range map[string]map[string]any{
		"sandbox-inherit-" + uuid.NewString(): {
			"runtimeId": runtimeID,
		},
		"sandbox-override-" + uuid.NewString(): {
			"runtimeId": runtimeID, "skillIds": []string{},
		},
		"sandbox-direct-" + uuid.NewString(): {
			"runtimeId": runtimeID, "skillIds": []string{skillID},
		},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
      (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
      VALUES ($1, $2, $3, $1, '', TRUE, $4::jsonb, NOW(), NOW())`,
			id, platform.KindSandbox, projectID, mustMapJSON(spec)); err != nil {
			t.Fatal(err)
		}
	}

	detail.Spec["instructions"] = strings.Replace(document, "Run the review.", "Run the updated review.", 1)
	updated, err := s.UpdateResource(ctx, skillID, detail.Input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Spec["bundleDigest"] == digest {
		t.Fatal("skill content update did not change bundle digest")
	}

	rows, err := s.pool.Query(ctx, `SELECT id, COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE), generation,
	    COALESCE((spec->>'capabilityRevision')::bigint, 0)
	    FROM control_resources WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	generations := make(map[string]int64)
	revisions := make(map[string]int64)
	for rows.Next() {
		var id string
		var pending bool
		var generation, revision int64
		if err := rows.Scan(&id, &pending, &generation, &revision); err != nil {
			t.Fatal(err)
		}
		generations[id] = generation
		revisions[id] = revision
		if strings.HasPrefix(id, "sandbox-override-") && pending {
			t.Fatalf("explicit override sandbox %s was marked pending", id)
		}
		if !strings.HasPrefix(id, "sandbox-override-") && !pending {
			t.Fatalf("referencing sandbox %s was not marked pending", id)
		}
		if generation != 1 {
			t.Fatalf("capability content update changed sandbox %s lifecycle generation to %d", id, generation)
		}
		if strings.HasPrefix(id, "sandbox-override-") && revision != 0 {
			t.Fatalf("explicit override sandbox %s capability revision = %d", id, revision)
		}
		if !strings.HasPrefix(id, "sandbox-override-") && revision == 0 {
			t.Fatalf("referencing sandbox %s capability revision was not advanced", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
    SET spec = spec - 'capabilitiesPendingRestart'
    WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	runtime.Spec["skillIds"] = []string{}
	runtime, err = s.UpdateResource(ctx, runtimeID, runtime.Input)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.pool.Query(ctx, `SELECT id, COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE), generation,
	    COALESCE((spec->>'capabilityRevision')::bigint, 0)
	    FROM control_resources WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pending bool
		var generation, revision int64
		if err := rows.Scan(&id, &pending, &generation, &revision); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(id, "sandbox-inherit-") != pending {
			t.Fatalf("Runtime update pending state for %s = %v", id, pending)
		}
		if generation != generations[id] {
			t.Fatalf("Runtime capability update changed lifecycle generation for %s to %d", id, generation)
		}
		wantRevision := revisions[id]
		if strings.HasPrefix(id, "sandbox-inherit-") {
			wantRevision++
		}
		if revision != wantRevision {
			t.Fatalf("Runtime update capability revision for %s = %d, want %d", id, revision, wantRevision)
		}
		revisions[id] = revision
	}
	rows.Close()
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	    SET spec = spec - 'capabilitiesPendingRestart'
	    WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	runtime.Spec["agentTools"] = []string{"codex"}
	if _, err := s.UpdateResource(ctx, runtimeID, runtime.Input); err != nil {
		t.Fatal(err)
	}
	rows, err = s.pool.Query(ctx, `SELECT id, COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE), generation,
	    COALESCE((spec->>'capabilityRevision')::bigint, 0)
	    FROM control_resources WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pending bool
		var generation, revision int64
		if err := rows.Scan(&id, &pending, &generation, &revision); err != nil {
			t.Fatal(err)
		}
		legacyInheritsAgentTools := !strings.HasPrefix(id, "sandbox-frozen-")
		if pending != legacyInheritsAgentTools {
			t.Fatalf("Runtime Agent target pending state for %s = %v", id, pending)
		}
		if generation != generations[id] {
			t.Fatalf("Runtime Agent target update changed lifecycle generation for %s to %d", id, generation)
		}
		wantRevision := revisions[id]
		if legacyInheritsAgentTools {
			wantRevision++
		}
		if revision != wantRevision {
			t.Fatalf("Runtime Agent target capability revision for %s = %d, want %d", id, revision, wantRevision)
		}
		revisions[id] = revision
	}
	rows.Close()
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	    SET spec = spec - 'capabilitiesPendingRestart'
	    WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	runtime, err = s.GetResource(ctx, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeEnvironment := runtime.Input
	runtimeEnvironment.Spec = maps.Clone(runtime.Spec)
	runtimeEnvironment.Spec["environmentVariables"] = []any{
		map[string]any{"name": "RUNTIME_FLAG", "value": "enabled"},
	}
	if _, err := s.UpdateResource(ctx, runtimeID, runtimeEnvironment); err != nil {
		t.Fatalf("Runtime restart-time environment update: %v", err)
	}
	rows, err = s.pool.Query(ctx, `SELECT id, COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE), generation,
	    COALESCE((spec->>'capabilityRevision')::bigint, 0)
	    FROM control_resources WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pending bool
		var generation, revision int64
		if err := rows.Scan(&id, &pending, &generation, &revision); err != nil {
			t.Fatal(err)
		}
		legacyInheritsEnvironment := !strings.HasPrefix(id, "sandbox-frozen-")
		if pending != legacyInheritsEnvironment {
			t.Fatalf("Runtime environment pending state for %s = %v", id, pending)
		}
		if generation != generations[id] {
			t.Fatalf("Runtime environment update changed lifecycle generation for %s to %d", id, generation)
		}
		wantRevision := revisions[id]
		if legacyInheritsEnvironment {
			wantRevision++
		}
		if revision != wantRevision {
			t.Fatalf("Runtime environment capability revision for %s = %d, want %d", id, revision, wantRevision)
		}
	}
	rows.Close()

	disabled := updated.Input
	disabled.Enabled = false
	if _, err := s.UpdateResource(ctx, skillID, disabled); !platform.IsValidationError(err) {
		t.Fatalf("disable referenced Skill error = %v", err)
	}
	otherProject := "project-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
    (id, kind, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, 'project', $1, '', TRUE, '{}'::jsonb, NOW(), NOW())`, otherProject); err != nil {
		t.Fatal(err)
	}
	moved := updated.Input
	moved.ProjectID = &otherProject
	if _, err := s.UpdateResource(ctx, skillID, moved); !errors.Is(err, ErrConflict) {
		t.Fatalf("move referenced Skill error = %v", err)
	}
}

func TestCapabilityBindingsRequireProjectVariablesAndNetwork(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	projectID := "default"
	secretVariable := platform.Input{
		ID: "variable-" + uuid.NewString(), Kind: platform.KindVariable, ProjectID: &projectID,
		Name: "MCP token", Enabled: true,
		Spec: map[string]any{"key": "MCP_TOKEN", "mode": "secret-ref", "reference": "secret://HOST_MCP_TOKEN"},
	}
	if _, err := s.CreateResource(ctx, secretVariable); err != nil {
		t.Fatal(err)
	}
	duplicateVariable := secretVariable
	duplicateVariable.ID = "variable-" + uuid.NewString()
	if _, err := s.CreateResource(ctx, duplicateVariable); err != nil {
		t.Fatal(err)
	}
	mcp := platform.Input{
		ID: "mcp-" + uuid.NewString(), Kind: platform.KindMCP, ProjectID: &projectID,
		Name: "Remote MCP", Enabled: true,
		Spec: map[string]any{
			"transport": "http", "url": "https://mcp.example.test/rpc",
			"headers": []any{map[string]any{"name": "Authorization", "valueFrom": "secret://MCP_TOKEN"}},
		},
	}
	if _, err := s.CreateResource(ctx, mcp); err != nil {
		t.Fatal(err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	base := map[string]any{
		"driver": "boxlite", "network": "restricted", "agentTools": []string{"codex"},
		"variableIds": []string{secretVariable.ID}, "mcpServerIds": []string{mcp.ID},
	}
	hosts, err := ensureCapabilityReferences(ctx, tx, &projectID, base, "测试环境")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0] != "mcp.example.test" {
		t.Fatalf("MCP allow-net hosts = %#v", hosts)
	}

	withoutVariable := maps.Clone(base)
	withoutVariable["variableIds"] = []string{}
	if _, err := ensureCapabilityReferences(ctx, tx, &projectID, withoutVariable, "测试环境"); !platform.IsValidationError(err) {
		t.Fatalf("missing Variable error = %v", err)
	}
	duplicateKey := maps.Clone(base)
	duplicateKey["variableIds"] = []string{secretVariable.ID, duplicateVariable.ID}
	if _, err := ensureCapabilityReferences(ctx, tx, &projectID, duplicateKey, "测试环境"); !platform.IsValidationError(err) {
		t.Fatalf("duplicate Variable key error = %v", err)
	}
	collision := maps.Clone(base)
	collision["environmentVariables"] = []any{map[string]any{"name": "MCP_TOKEN", "value": "encrypted"}}
	if _, err := ensureCapabilityReferences(ctx, tx, &projectID, collision, "测试环境"); !platform.IsValidationError(err) {
		t.Fatalf("Variable/environment collision error = %v", err)
	}
	isolated := maps.Clone(base)
	isolated["network"] = "none"
	if _, err := ensureCapabilityReferences(ctx, tx, &projectID, isolated, "测试环境"); !platform.IsValidationError(err) {
		t.Fatalf("isolated remote MCP error = %v", err)
	}

	otherProject := "project-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
    (id, kind, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, 'project', $1, '', TRUE, '{}'::jsonb, NOW(), NOW())`, otherProject); err != nil {
		t.Fatal(err)
	}
	foreignVariable := secretVariable
	foreignVariable.ID = "variable-" + uuid.NewString()
	foreignVariable.ProjectID = &otherProject
	if _, err := s.CreateResource(ctx, foreignVariable); err != nil {
		t.Fatal(err)
	}
	foreign := maps.Clone(base)
	foreign["variableIds"] = []string{foreignVariable.ID}
	if _, err := ensureCapabilityReferences(ctx, tx, &projectID, foreign, "测试环境"); !platform.IsValidationError(err) {
		t.Fatalf("cross-project Variable error = %v", err)
	}
}

func TestFrozenBoxLiteRestrictedMCPHostsRequireNewSandbox(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	projectID := "default"
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatal(err)
	}
	inventory := platform.ServerInventory{DockerImages: []platform.ServerImage{{Reference: "ubuntu:24.04", Architecture: "amd64"}}}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, []string{"boxlite", workerFailClosedJobOutputCapability}, &inventory, "test"); err != nil {
		t.Fatal(err)
	}
	createMCP := func(name string, spec map[string]any) platform.Resource {
		t.Helper()
		resource, err := s.CreateResource(ctx, platform.Input{
			ID: "mcp-" + uuid.NewString(), Kind: platform.KindMCP, ProjectID: &projectID,
			Name: name, Enabled: true, Spec: spec,
		})
		if err != nil {
			t.Fatal(err)
		}
		return resource
	}
	alpha := createMCP("Alpha MCP", map[string]any{
		"transport": "http", "url": "https://alpha.example.test/rpc",
	})
	alphaPeer := createMCP("Alpha peer MCP", map[string]any{
		"transport": "http", "url": "https://alpha.example.test/other",
	})
	beta := createMCP("Beta MCP", map[string]any{
		"transport": "http", "url": "https://beta.example.test/rpc",
	})
	stdio := createMCP("stdio MCP", map[string]any{
		"transport": "stdio", "command": "node", "args": []string{"server.js"},
	})
	runtimeID := "runtime-" + uuid.NewString()
	runtime, err := s.CreateResource(ctx, platform.Input{
		ID: runtimeID, Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "BoxLite MCP runtime", Enabled: true,
		Spec: map[string]any{
			"serverId": serverID, "driver": "boxlite", "imageReference": "ubuntu:24.04",
			"network": "restricted", "agentTools": []string{"codex"}, "mcpServerIds": []string{alpha.ID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacySandboxID := "sandbox-legacy-boxlite-" + uuid.NewString()
	explicitSandboxID := "sandbox-explicit-boxlite-" + uuid.NewString()
	stdioSandboxID := "sandbox-stdio-boxlite-" + uuid.NewString()
	for id, spec := range map[string]map[string]any{
		legacySandboxID: {
			"serverId": serverID, "runtimeId": runtimeID, "agentTools": []string{"codex"}, "appliedProxyId": "",
		},
		explicitSandboxID: {
			"serverId": serverID, "runtimeId": runtimeID, "agentTools": []string{"codex"},
			"mcpServerIds": []string{alpha.ID}, "appliedProxyId": "",
		},
		stdioSandboxID: {
			"serverId": serverID, "runtimeId": runtimeID, "agentTools": []string{"codex"},
			"mcpServerIds": []string{stdio.ID}, "appliedProxyId": "",
		},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	      (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	      VALUES ($1, 'sandbox', $2, $1, '', TRUE, $3::jsonb, NOW(), NOW())`,
			id, projectID, mustMapJSON(spec)); err != nil {
			t.Fatal(err)
		}
	}

	explicitSandbox, err := s.GetResource(ctx, explicitSandboxID)
	if err != nil {
		t.Fatal(err)
	}
	differentHost := explicitSandbox.Input
	differentHost.Spec = maps.Clone(explicitSandbox.Spec)
	differentHost.Spec["mcpServerIds"] = []string{beta.ID}
	if _, err := s.UpdateResource(ctx, explicitSandboxID, differentHost); !platform.IsValidationError(err) || !strings.Contains(err.Error(), "请新建沙箱") {
		t.Fatalf("BoxLite sandbox host-set update error = %v", err)
	}
	sameHost := explicitSandbox.Input
	sameHost.Spec = maps.Clone(explicitSandbox.Spec)
	sameHost.Spec["mcpServerIds"] = []string{alphaPeer.ID}
	updatedSandbox, err := s.UpdateResource(ctx, explicitSandboxID, sameHost)
	if err != nil {
		t.Fatalf("same-host BoxLite sandbox MCP update: %v", err)
	}
	if pending, _ := updatedSandbox.Spec["capabilitiesPendingRestart"].(bool); !pending {
		t.Fatal("same-host sandbox binding update was not marked pending restart")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	    SET spec = spec - 'capabilitiesPendingRestart' WHERE id = $1`, explicitSandboxID); err != nil {
		t.Fatal(err)
	}
	agentTargetUpdate := updatedSandbox.Input
	agentTargetUpdate.Spec = maps.Clone(updatedSandbox.Spec)
	agentTargetUpdate.Spec["agentTools"] = []string{"claude-code"}
	updatedSandbox, err = s.UpdateResource(ctx, explicitSandboxID, agentTargetUpdate)
	if err != nil {
		t.Fatalf("sandbox Agent target update: %v", err)
	}
	if pending, _ := updatedSandbox.Spec["capabilitiesPendingRestart"].(bool); !pending {
		t.Fatal("sandbox Agent target update was not marked pending restart")
	}

	alphaSameHost := alpha.Input
	alphaSameHost.Spec = maps.Clone(alpha.Spec)
	alphaSameHost.Spec["url"] = "https://alpha.example.test/v2"
	alpha, err = s.UpdateResource(ctx, alpha.ID, alphaSameHost)
	if err != nil {
		t.Fatalf("same-host MCP URL update: %v", err)
	}
	alphaDifferentHost := alpha.Input
	alphaDifferentHost.Spec = maps.Clone(alpha.Spec)
	alphaDifferentHost.Spec["url"] = "https://changed.example.test/rpc"
	if _, err := s.UpdateResource(ctx, alpha.ID, alphaDifferentHost); !platform.IsValidationError(err) || !strings.Contains(err.Error(), "请新建沙箱") {
		t.Fatalf("in-use MCP hostname update error = %v", err)
	}

	runtimeSameHost := runtime.Input
	runtimeSameHost.Spec = maps.Clone(runtime.Spec)
	runtimeSameHost.Spec["mcpServerIds"] = []string{alphaPeer.ID}
	runtime, err = s.UpdateResource(ctx, runtimeID, runtimeSameHost)
	if err != nil {
		t.Fatalf("same-host legacy Runtime MCP update: %v", err)
	}
	runtimeDifferentHost := runtime.Input
	runtimeDifferentHost.Spec = maps.Clone(runtime.Spec)
	runtimeDifferentHost.Spec["mcpServerIds"] = []string{beta.ID}
	if _, err := s.UpdateResource(ctx, runtimeID, runtimeDifferentHost); !platform.IsValidationError(err) || !strings.Contains(err.Error(), "请新建沙箱") {
		t.Fatalf("legacy Runtime MCP host-set update error = %v", err)
	}

	stdioUpdate := stdio.Input
	stdioUpdate.Spec = maps.Clone(stdio.Spec)
	stdioUpdate.Spec["args"] = []string{"server.js", "--verbose"}
	if _, err := s.UpdateResource(ctx, stdio.ID, stdioUpdate); err != nil {
		t.Fatalf("stdio MCP config update on BoxLite restricted sandbox: %v", err)
	}

	networkUpdate := runtime.Input
	networkUpdate.Spec = maps.Clone(runtime.Spec)
	networkUpdate.Spec["network"] = "egress"
	if _, err := s.UpdateResource(ctx, runtimeID, networkUpdate); !platform.IsValidationError(err) ||
		!strings.Contains(err.Error(), "network") || !strings.Contains(err.Error(), "请新建沙箱") {
		t.Fatalf("legacy Sandbox inherited network update error = %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	    SET spec = jsonb_set(spec, '{network}', '"restricted"'::jsonb, TRUE)
	    WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateResource(ctx, runtimeID, networkUpdate); err != nil {
		t.Fatalf("Runtime network update with frozen Sandbox snapshots: %v", err)
	}
}
