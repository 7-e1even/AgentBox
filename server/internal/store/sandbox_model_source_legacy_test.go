package store

import (
	"errors"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestExplicitSandboxRuntimeModelSourcesRequiresCompleteLocalBindings(t *testing.T) {
	complete := map[string]any{
		"credentialIds": []any{"credential-one"},
		"modelBindings": map[string]any{"credential-one": "model-one"},
	}
	sources, ok := explicitSandboxRuntimeModelSources(complete, time.Time{})
	if !ok || sources["credential-one"].ModelID != "model-one" {
		t.Fatalf("explicitSandboxRuntimeModelSources() = %#v, %v", sources, ok)
	}

	for _, spec := range []map[string]any{
		{"credentialIds": []any{"credential-one"}},
		{"modelBindings": map[string]any{"credential-one": "model-one"}},
		{"credentialIds": "credential-one", "modelBindings": map[string]any{"credential-one": "model-one"}},
		{"credentialIds": []any{"credential-one"}, "modelBindings": []any{"model-one"}},
		{"credentialIds": []any{"credential-one", 2}, "modelBindings": map[string]any{"credential-one": "model-one"}},
		{"credentialIds": []any{"credential-one"}, "modelBindings": map[string]any{"credential-one": 2}},
		{"credentialIds": []any{" "}, "modelBindings": map[string]any{" ": "model-one"}},
		{"credentialIds": []any{"credential-one"}, "modelBindings": map[string]any{"credential-one": " "}},
		{"credentialIds": []any{"credential-one"}, "modelBindings": map[string]any{}},
		{
			"credentialIds": []any{"credential-one", "credential-one"},
			"modelBindings": map[string]any{"credential-one": "model-one", "extra": "model-two"},
		},
		{
			"credentialIds": []any{"credential-one"},
			"modelBindings": map[string]any{"credential-one": "model-one", "extra": "model-two"},
		},
	} {
		if sources, ok := explicitSandboxRuntimeModelSources(spec, time.Time{}); ok {
			t.Fatalf("incomplete explicit sandbox sources were accepted: %#v from %#v", sources, spec)
		}
	}
}

func TestCreateSandboxFreezesInheritedModelBindingsAndLegacyFallbackStaysConservative(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register legacy binding Worker: %v", err)
	}
	inventory := platform.ServerInventory{
		DockerImages: []platform.ServerImage{{Reference: "ubuntu:24.04", Architecture: "amd64"}},
	}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, []string{"docker"}, &inventory, "test"); err != nil {
		t.Fatalf("heartbeat legacy binding Worker: %v", err)
	}

	credentialID := "legacy-binding-" + uuid.NewString()
	const oldModelID = "legacy-old-model"
	const newModelID = "legacy-new-model"
	insertRuntimeModelSourceCredential(
		t, s, credentialID, "openai", "openai-chat", "https://legacy.example.test/v1", oldModelID, "legacy-secret",
	)
	if _, err := s.pool.Exec(ctx, `UPDATE provider_credentials SET models = jsonb_build_array(
	  jsonb_build_object('id', $1::text, 'name', $1::text, 'source', 'manual'),
	  jsonb_build_object('id', $2::text, 'name', $2::text, 'source', 'manual')
	) WHERE id = $3`, oldModelID, newModelID, credentialID); err != nil {
		t.Fatalf("add legacy binding models: %v", err)
	}

	projectID := "default"
	runtimeID := "legacy-runtime-" + uuid.NewString()
	if _, err := s.CreateResource(ctx, platform.Input{
		ID: runtimeID, Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "Legacy binding runtime", Enabled: true,
		Spec: map[string]any{
			"serverId": serverID, "driver": "docker", "imageReference": "ubuntu:24.04",
			"credentialIds": []string{credentialID},
			"modelBindings": map[string]string{credentialID: oldModelID},
		},
	}); err != nil {
		t.Fatalf("create legacy binding runtime: %v", err)
	}

	sandboxID := "legacy-sandbox-" + uuid.NewString()
	sandbox, err := s.CreateResource(ctx, platform.Input{
		ID: sandboxID, Kind: platform.KindSandbox, ProjectID: &projectID,
		Name: "Legacy binding sandbox", Enabled: true,
		Spec: map[string]any{"serverId": serverID, "runtimeId": runtimeID},
	})
	if err != nil {
		t.Fatalf("create inherited binding sandbox: %v", err)
	}
	if bindings := specStringMap(sandbox.Spec, "modelBindings"); bindings[credentialID] != oldModelID {
		t.Fatalf("created sandbox did not freeze inherited model bindings: %#v", sandbox.Spec["modelBindings"])
	}

	if _, err := s.pool.Exec(ctx, "DELETE FROM worker_jobs WHERE resource_id = $1", sandboxID); err != nil {
		t.Fatalf("remove legacy lifecycle history: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET
	  spec = (spec - 'runtimeModelSources' - 'runtimeModelSourcesComplete' - 'runtimeModelTokenEpoch')
	    || jsonb_build_object(
	      'status', 'running',
	      'modelBindings', jsonb_build_object($1::text, $2::text, 'extra-slot', $2::text)
	    ),
	  observed_generation = generation
	  WHERE id = $3`, credentialID, newModelID, sandboxID); err != nil {
		t.Fatalf("downgrade sandbox to legacy binding state: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET
	  spec = jsonb_set(spec, '{modelBindings}', jsonb_build_object($1::text, $2::text), TRUE)
	  WHERE id = $3`, credentialID, newModelID, runtimeID); err != nil {
		t.Fatalf("change inherited runtime binding: %v", err)
	}

	token, err := s.issueRuntimeLLMToken(
		sandboxID, credentialID, oldModelID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue legacy binding token: %v", err)
	}
	target, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, credentialID, token)
	if err != nil {
		t.Fatalf("resolve signed legacy binding after template drift: %v", err)
	}
	if target.CredentialID != credentialID || target.ModelID != oldModelID || target.Secret != "legacy-secret" {
		t.Fatalf("legacy binding resolved to changed template source: %#v", target)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     credentialID,
		CredentialID:         credentialID,
		ModelID:              newModelID,
		ExpectedCredentialID: credentialID,
		ExpectedModelID:      oldModelID,
	}); !platform.IsValidationError(err) {
		t.Fatalf("unproven legacy binding source switch error = %v, want validation error", err)
	}
	if _, err := s.DeleteCredentialModel(ctx, credentialID, oldModelID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete unproven legacy binding model error = %v, want ErrConflict", err)
	}
}
