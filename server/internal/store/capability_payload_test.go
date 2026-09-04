package store

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestPrepareMCPWorkerDefinitionsGatesManagedConfiguration(t *testing.T) {
	legacy := []map[string]any{{"id": "stdio", "spec": map[string]any{
		"transport": "stdio", "command": "npx", "args": []any{"-y", "example"},
	}}}
	if err := prepareMCPWorkerDefinitions(legacy, false, true); err != nil {
		t.Fatal(err)
	}
	legacySpec := legacy[0]["spec"].(map[string]any)
	if legacySpec["args"] != "-y example" {
		t.Fatalf("legacy args = %#v", legacySpec["args"])
	}
	if _, exists := legacySpec["arguments"]; exists {
		t.Fatal("managed arguments were sent to a legacy Worker")
	}

	managed := []map[string]any{{"id": "http", "spec": map[string]any{
		"transport": "http", "url": "https://mcp.example.test", "headers": []any{
			map[string]any{"name": "Authorization", "valueFrom": "secret://MCP_TOKEN"},
		},
	}}}
	if err := prepareMCPWorkerDefinitions(managed, true, false); err != nil {
		t.Fatal(err)
	}
	managedSpec := managed[0]["spec"].(map[string]any)
	if _, exists := managedSpec["headers"]; !exists {
		t.Fatal("managed headers were not sent to a capable Worker")
	}
	if _, exists := managedSpec["args"]; exists {
		t.Fatal("managed Worker payload included ambiguous legacy args")
	}

	unsupportedHeaders := []map[string]any{{"id": "http", "spec": map[string]any{
		"transport": "http", "url": "https://mcp.example.test", "headers": []any{
			map[string]any{"name": "Authorization", "valueFrom": "secret://MCP_TOKEN"},
		},
	}}}
	if err := prepareMCPWorkerDefinitions(unsupportedHeaders, false, true); !platform.IsValidationError(err) {
		t.Fatalf("legacy Worker header error = %v", err)
	}

	spacedArgs := []map[string]any{{"id": "stdio", "spec": map[string]any{
		"transport": "stdio", "command": "mcp", "args": []any{"--label", "two words"},
	}}}
	if err := prepareMCPWorkerDefinitions(spacedArgs, false, true); !platform.IsValidationError(err) {
		t.Fatalf("legacy Worker spaced args error = %v", err)
	}
	if err := prepareMCPWorkerDefinitions(spacedArgs, true, false); err != nil {
		t.Fatalf("managed Worker rejected canonical args: %v", err)
	}

	legacyCwd := []map[string]any{{"id": "stdio", "spec": map[string]any{
		"transport": "stdio", "command": "mcp", "cwd": "/workspace/service",
	}}}
	if err := prepareMCPWorkerDefinitions(legacyCwd, false, true); !platform.IsValidationError(err) {
		t.Fatalf("legacy Worker cwd error = %v", err)
	}
	if err := prepareMCPWorkerDefinitions(legacyCwd, true, false); err != nil {
		t.Fatalf("managed Worker rejected cwd: %v", err)
	}
	if err := prepareMCPWorkerDefinitions(legacy, false, false); !platform.IsValidationError(err) {
		t.Fatalf("existing sandbox legacy MCP error = %v", err)
	}
}

func TestManagedCapabilitySupportGate(t *testing.T) {
	if err := validateManagedCapabilitySupport(map[string]any{
		"variableIds": []string{"token"},
	}, true, false); !platform.IsValidationError(err) {
		t.Fatalf("legacy Worker Variable create error = %v", err)
	}
	if err := validateManagedCapabilitySupport(map[string]any{
		"capabilitiesPendingRestart": true,
	}, false, false); !platform.IsValidationError(err) {
		t.Fatalf("legacy Worker pending restart error = %v", err)
	}
	if err := validateManagedCapabilitySupport(map[string]any{
		"skillIds": []string{"review"},
	}, true, false); err != nil {
		t.Fatalf("fresh legacy Worker Skill install error = %v", err)
	}
	if err := validateManagedCapabilitySupport(map[string]any{
		"variableIds": []string{"token"}, "capabilitiesPendingRestart": true,
	}, false, true); err != nil {
		t.Fatalf("managed Worker capability gate error = %v", err)
	}
}

func TestSandboxDesiredSpecsEqualIgnoresObservationsOnly(t *testing.T) {
	current := map[string]any{
		"runtimeId": "runtime-one", "agentTools": []any{"codex"},
		"status": "running", "capabilitiesPendingRestart": false,
		"capabilityDigest": "sha256:old",
	}
	next := map[string]any{"runtimeId": "runtime-one", "agentTools": []string{"codex"}}
	if !sandboxDesiredSpecsEqual(current, next) {
		t.Fatal("observed fields changed desired-spec equality")
	}
	changed := maps.Clone(next)
	changed["agentTools"] = []string{"claude-code"}
	if sandboxDesiredSpecsEqual(current, changed) {
		t.Fatal("real sandbox configuration change was treated as metadata-only")
	}
}

func TestCompactWorkerJobPayloadRemovesCapabilitySecretsAndBundles(t *testing.T) {
	payload := map[string]any{
		"sandboxId": "sandbox-one",
		"environmentVariables": []any{
			map[string]any{"name": "TOKEN", "value": "encrypted-or-plain-secret"},
		},
		"skills": []any{map[string]any{
			"id": "review", "spec": map[string]any{
				"bundleDigest": "sha256:digest", "instructions": "sensitive instructions",
				"files": []any{map[string]any{"path": "secret", "content": "c2VjcmV0"}},
			},
		}},
		"mcpServers": []any{map[string]any{
			"id": "remote", "spec": map[string]any{
				"transport": "http", "headers": []any{map[string]any{"name": "Authorization", "valueFrom": "secret://MCP_TOKEN"}},
			},
		}},
		"variables": []any{map[string]any{
			"id": "token", "spec": map[string]any{"key": "MCP_TOKEN", "reference": "secret://HOST_TOKEN"},
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	compacted, err := compactWorkerJobPayload(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text := string(compacted)
	for _, forbidden := range []string{"encrypted-or-plain-secret", "sensitive instructions", "c2VjcmV0", "secret://MCP_TOKEN", "secret://HOST_TOKEN"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("compacted payload retained %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"sandbox-one", "review", "sha256:digest", "remote", "token"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("compacted payload lost %q: %s", expected, text)
		}
	}
}

func TestSandboxCapabilityDigestIsDeterministic(t *testing.T) {
	first := map[string]any{
		"agentTools": []any{"codex"},
		"skills":     []any{map[string]any{"id": "review", "spec": map[string]any{"bundleDigest": "sha256:a"}}},
		"mcpServers": []any{}, "variables": []any{},
	}
	second := map[string]any{
		"agentTools": []any{"codex"},
		"variables":  []any{}, "mcpServers": []any{},
		"skills": []any{map[string]any{"spec": map[string]any{"bundleDigest": "sha256:a"}, "id": "review"}},
	}
	if sandboxCapabilityDigest(first) != sandboxCapabilityDigest(second) {
		t.Fatal("capability digest depends on map insertion order")
	}
	third := maps.Clone(second)
	third["agentTools"] = []any{"claude-code"}
	if sandboxCapabilityDigest(first) == sandboxCapabilityDigest(third) {
		t.Fatal("capability digest ignores Agent targets")
	}
}

func TestSandboxCapabilityRevisionUsesOnlyCanonicalNonNegativeIntegers(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  int64
	}{
		{name: "missing", want: 0},
		{name: "new resource integer", value: int64(1), want: 1},
		{name: "database decoded number", value: float64(7), want: 7},
		{name: "fractional", value: 1.5, want: 0},
		{name: "negative", value: -1, want: 0},
		{name: "string", value: "3", want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sandboxCapabilityRevision(map[string]any{"capabilityRevision": test.value}); got != test.want {
				t.Fatalf("revision = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSandboxCreationValuesEqualUsesEffectiveImageAndNetwork(t *testing.T) {
	for _, test := range []struct {
		name    string
		key     string
		current map[string]any
		next    map[string]any
		want    bool
	}{
		{
			name: "unused image id does not change resolved reference", key: "imageId", want: true,
			current: map[string]any{"imageReference": "ubuntu:24.04", "imageId": "old"},
			next:    map[string]any{"imageReference": "ubuntu:24.04", "imageId": "new"},
		},
		{
			name: "resolved image reference changes", key: "imageReference",
			current: map[string]any{"imageReference": "ubuntu:22.04", "imageId": "same"},
			next:    map[string]any{"imageReference": "ubuntu:24.04", "imageId": "same"},
		},
		{
			name: "boxlite default equals explicit restricted", key: "network", want: true,
			current: map[string]any{"driver": "boxlite", "network": ""},
			next:    map[string]any{"driver": "boxlite", "network": "restricted"},
		},
		{
			name: "effective BoxLite network changes", key: "network",
			current: map[string]any{"driver": "boxlite", "network": "restricted"},
			next:    map[string]any{"driver": "boxlite", "network": "egress"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sandboxCreationValuesEqual(test.key, test.current, test.next); got != test.want {
				t.Fatalf("equal = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSandboxCreationFieldsCoverWorkerCreateOnlyConfiguration(t *testing.T) {
	want := map[string]bool{
		"runtimeId": true, "serverId": true, "driver": true,
		"imageReference": true, "imageId": true, "cpu": true, "memory": true,
		"desktop": true, "network": true, "workdir": true, "setup": true,
		"workspace": true, "extensionIds": true,
	}
	if len(sandboxCreationFields) != len(want) {
		t.Fatalf("creation fields = %v, want exactly %v", sandboxCreationFields, want)
	}
	for _, field := range sandboxCreationFields {
		if !want[field] {
			t.Fatalf("unexpected creation field %q in %v", field, sandboxCreationFields)
		}
		delete(want, field)
	}
	if len(want) != 0 {
		t.Fatalf("missing creation fields: %v", want)
	}
}

func TestMCPHeaderMigrationPreservesReferencesAndAuditsRemoval(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/0010_remove_legacy_mcp_headers.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, expected := range []string{
		"jsonb_agg(jsonb_build_object(",
		"'valueFrom'",
		"mcp-headers-removed",
		"mcp-url-removed",
		"spec = spec - 'headers'",
		"spec = resource.spec - 'url'",
		"'value-ref'",
		"'secret-ref'",
		"'shape'",
		"migration_0010_affected_sandbox_ids",
		"migration_0010_historical_sandbox_sink_ids",
		"migration_0010_historical_automation_sink_ids",
		"worker_jobs",
		"{spec,headers}",
		"capabilityRevision",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("migration is missing %q", expected)
		}
	}
	if strings.Contains(sql, "SET spec = spec - 'headers'\nWHERE kind = 'mcp' AND spec ? 'headers';") {
		t.Fatal("migration unconditionally deletes every MCP header")
	}
	for remaining := sql; ; {
		index := strings.Index(remaining, "jsonb_array_elements(")
		if index < 0 {
			break
		}
		remaining = remaining[index+len("jsonb_array_elements("):]
		window := remaining
		if len(window) > 200 {
			window = window[:200]
		}
		if !strings.Contains(window, "CASE WHEN") {
			t.Fatal("migration expands JSON without a type-guarded empty-array fallback")
		}
	}
}

func TestHistoricalWorkerSinkMigrationDoesNotDependOnRetainedJobs(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/0010_remove_legacy_mcp_headers.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, expected := range []string{
		"INSERT INTO migration_0010_historical_sandbox_sink_ids(id)\nSELECT sandbox.id\nFROM control_resources sandbox",
		"SELECT 1 FROM migration_0010_historical_sandbox_sink_ids affected",
		"INSERT INTO migration_0010_historical_automation_sink_ids(id)\nSELECT run.id\nFROM automation_runs run",
		"OR run.error_stage <> ''",
		"JOIN migration_0010_historical_automation_sink_ids affected",
		"result_error_stage = safe.safe_stage",
		"error_stage = safe.safe_stage",
		"ELSE 'create'",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("historical sink migration is missing independent scan %q", expected)
		}
	}
}
