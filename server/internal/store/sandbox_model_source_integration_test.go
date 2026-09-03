package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func insertRuntimeModelSourceCredential(
	t *testing.T,
	s *Store,
	id, providerID, protocol, endpoint, modelID, secret string,
) {
	t.Helper()
	ciphertext, nonce, err := encryptSecret(s.secretKey, secret)
	if err != nil {
		t.Fatalf("encrypt runtime model source credential: %v", err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO provider_credentials
	  (id, name, provider_id, protocol, endpoint, models, secret_ciphertext,
	   secret_nonce, secret_last_four, enabled, created_at, updated_at)
	  VALUES ($1, $1, $2, $3, $4,
	    jsonb_build_array(jsonb_build_object('id', $5::text, 'name', $5::text, 'source', 'manual')),
	    $6, $7, 'test', TRUE, NOW(), NOW())`,
		id, providerID, protocol, endpoint, modelID, ciphertext, nonce,
	); err != nil {
		t.Fatalf("insert runtime model source credential: %v", err)
	}
}

func TestSandboxModelSourceSwitchKeepsRuntimeTokenAndProtectsTarget(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	s.catalog = catalog.BuiltinCatalog
	originalCredentialID := "source-original-" + uuid.NewString()
	targetCredentialID := "source-target-" + uuid.NewString()
	originalModelID := "original-model"
	targetModelID := "target-model"
	originalSecret := "original-secret"
	targetSecret := "target-secret"
	initialTokenEpoch := uuid.NewString()
	insertRuntimeModelSourceCredential(
		t, s, originalCredentialID, "openai", "openai-chat", "https://original.example.test/v1", originalModelID, originalSecret,
	)
	insertRuntimeModelSourceCredential(
		t, s, targetCredentialID, "anthropic", "anthropic", "https://target.example.test/v1", targetModelID, targetSecret,
	)
	sandboxID := "sandbox-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at,
	   generation, observed_generation)
	  VALUES ($1, 'sandbox', 'default', 'Runtime model source sandbox', '', TRUE,
	    jsonb_build_object(
	      'status', 'running',
	      'credentialIds', jsonb_build_array($2::text),
	      'modelBindings', jsonb_build_object($2::text, $3::text),
	      'runtimeModelSources', jsonb_build_object(
	        $2::text, jsonb_build_object(
	          'credentialId', $2::text, 'modelId', $3::text,
	          'updatedAt', NOW()
	        )
	      ),
	      'runtimeModelSourcesComplete', TRUE,
	      'runtimeModelTokenEpoch', $4::text
	    ), NOW(), NOW(), 7, 7)`, sandboxID, originalCredentialID, originalModelID, initialTokenEpoch); err != nil {
		t.Fatalf("insert runtime model source sandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM audit_outbox WHERE entry->>'resourceId' = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM system_logs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM provider_credentials WHERE id = ANY($1)", []string{originalCredentialID, targetCredentialID})
	})

	issuedAt := time.Now().UTC().Add(-31 * 24 * time.Hour)
	token, err := s.issueRuntimeLLMTokenForEpoch(
		sandboxID, originalCredentialID, originalModelID, initialTokenEpoch, issuedAt.Add(runtimeLLMTokenTTL),
	)
	if err != nil {
		t.Fatalf("issue runtime model source token: %v", err)
	}
	assertTarget := func(wantCredentialID, wantModelID, wantSecret string) {
		t.Helper()
		target, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, originalCredentialID, token)
		if err != nil {
			t.Fatalf("resolve runtime model source target: %v", err)
		}
		if target.CredentialID != wantCredentialID || target.ModelID != wantModelID || target.Secret != wantSecret {
			t.Fatalf("resolved target = %#v, want credential=%s model=%s", target, wantCredentialID, wantModelID)
		}
	}
	assertTarget(originalCredentialID, originalModelID, originalSecret)

	// Desired edits configure a later restart. They must not revoke a signed token
	// already installed in the running sandbox.
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec || jsonb_build_object(
	  'credentialIds', '[]'::jsonb, 'modelBindings', '{}'::jsonb
	) WHERE id = $1`, sandboxID); err != nil {
		t.Fatalf("edit desired model bindings: %v", err)
	}
	assertTarget(originalCredentialID, originalModelID, originalSecret)
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec || jsonb_build_object(
	  'credentialIds', jsonb_build_array($1::text),
	  'modelBindings', jsonb_build_object($1::text, $2::text)
	) WHERE id = $3`, originalCredentialID, originalModelID, sandboxID); err != nil {
		t.Fatalf("restore desired model bindings: %v", err)
	}

	var jobsBefore int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM worker_jobs WHERE resource_id = $1", sandboxID).Scan(&jobsBefore); err != nil {
		t.Fatalf("count Worker jobs before model source switch: %v", err)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     originalCredentialID,
		CredentialID:         targetCredentialID,
		ModelID:              "missing-model",
		ExpectedCredentialID: originalCredentialID,
		ExpectedModelID:      originalModelID,
	}); !platform.IsValidationError(err) {
		t.Fatalf("missing target model error = %v, want validation error", err)
	}

	var switched platform.Resource
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		var err error
		switched, err = s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
			SlotCredentialID:     originalCredentialID,
			CredentialID:         targetCredentialID,
			ModelID:              targetModelID,
			ExpectedCredentialID: originalCredentialID,
			ExpectedModelID:      originalModelID,
		})
		return err
	}); err != nil {
		t.Fatalf("switch model source after lock release: %v", err)
	}
	if switched.Generation != 7 || switched.ObservedGeneration != 7 {
		t.Fatalf("switch changed reconciliation generations: generation=%d observed=%d", switched.Generation, switched.ObservedGeneration)
	}
	source, ok := sandboxRuntimeModelSource(switched.Spec, originalCredentialID)
	if !ok || source.CredentialID != targetCredentialID || source.ModelID != targetModelID || source.UpdatedAt.IsZero() {
		t.Fatalf("runtime model source = %#v, present=%v", source, ok)
	}
	assertTarget(targetCredentialID, targetModelID, targetSecret)
	var auditsBeforeConflict int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_outbox
	  WHERE entry->>'resourceId' = $1 AND entry->>'action' = 'switch-model-source'`, sandboxID).Scan(&auditsBeforeConflict); err != nil {
		t.Fatalf("count model source audits before stale switch: %v", err)
	}
	if auditsBeforeConflict != 1 {
		t.Fatalf("model source audits before stale switch = %d, want 1", auditsBeforeConflict)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     originalCredentialID,
		CredentialID:         originalCredentialID,
		ModelID:              originalModelID,
		ExpectedCredentialID: originalCredentialID,
		ExpectedModelID:      originalModelID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale model source switch error = %v, want ErrConflict", err)
	}
	assertTarget(targetCredentialID, targetModelID, targetSecret)
	var auditsAfterConflict int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_outbox
	  WHERE entry->>'resourceId' = $1 AND entry->>'action' = 'switch-model-source'`, sandboxID).Scan(&auditsAfterConflict); err != nil {
		t.Fatalf("count model source audits after stale switch: %v", err)
	}
	if auditsAfterConflict != auditsBeforeConflict {
		t.Fatalf("stale model source switch changed audit count from %d to %d", auditsBeforeConflict, auditsAfterConflict)
	}

	var jobsAfter int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM worker_jobs WHERE resource_id = $1", sandboxID).Scan(&jobsAfter); err != nil {
		t.Fatalf("count Worker jobs after model source switch: %v", err)
	}
	if jobsAfter != jobsBefore {
		t.Fatalf("Worker jobs changed from %d to %d", jobsBefore, jobsAfter)
	}
	if switched.Spec["runtimeModelTokenEpoch"] != initialTokenEpoch {
		t.Fatalf("hot switch changed token epoch to %#v", switched.Spec["runtimeModelTokenEpoch"])
	}
	legacyToken, err := s.issueRuntimeLLMToken(
		sandboxID, originalCredentialID, originalModelID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue legacy runtime token: %v", err)
	}
	rotatedEpoch := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET
	  spec = spec || jsonb_build_object('runtimeModelTokenEpoch', $1::text)
	  WHERE id = $2`, rotatedEpoch, sandboxID); err != nil {
		t.Fatalf("rotate runtime token epoch: %v", err)
	}
	for name, staleToken := range map[string]string{"previous epoch": token, "legacy epoch": legacyToken} {
		if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, originalCredentialID, staleToken); !errors.Is(err, ErrRuntimeUnauthorized) {
			t.Fatalf("%s token error = %v, want ErrRuntimeUnauthorized", name, err)
		}
	}
	rotatedToken, err := s.issueRuntimeLLMTokenForEpoch(
		sandboxID, originalCredentialID, originalModelID, rotatedEpoch, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue rotated runtime token: %v", err)
	}
	token = rotatedToken
	assertTarget(targetCredentialID, targetModelID, targetSecret)
	removedSlotToken, err := s.issueRuntimeLLMToken(
		sandboxID, targetCredentialID, targetModelID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue removed-slot token: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, targetCredentialID, removedSlotToken); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("removed runtime slot token error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     targetCredentialID,
		CredentialID:         targetCredentialID,
		ModelID:              targetModelID,
		ExpectedCredentialID: targetCredentialID,
		ExpectedModelID:      targetModelID,
	}); !platform.IsValidationError(err) {
		t.Fatalf("missing snapshot slot switch error = %v, want validation error", err)
	}

	var slotID, fromCredentialID, fromModelID, toCredentialID, toModelID string
	if err := s.pool.QueryRow(ctx, `SELECT
	  entry->'detail'->>'slotCredentialId', entry->'detail'->>'fromCredentialId',
	  entry->'detail'->>'fromModelId', entry->'detail'->>'toCredentialId',
	  entry->'detail'->>'toModelId'
	  FROM audit_outbox
	  WHERE entry->>'resourceId' = $1 AND entry->>'action' = 'switch-model-source'
	  ORDER BY created_at DESC LIMIT 1`, sandboxID).Scan(
		&slotID, &fromCredentialID, &fromModelID, &toCredentialID, &toModelID,
	); err != nil {
		t.Fatalf("load model source audit: %v", err)
	}
	if slotID != originalCredentialID || fromCredentialID != originalCredentialID || fromModelID != originalModelID ||
		toCredentialID != targetCredentialID || toModelID != targetModelID {
		t.Fatalf("model source audit = %q %q/%q -> %q/%q", slotID, fromCredentialID, fromModelID, toCredentialID, toModelID)
	}

	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     originalCredentialID,
		CredentialID:         originalCredentialID,
		ModelID:              originalModelID,
		ExpectedCredentialID: targetCredentialID,
		ExpectedModelID:      targetModelID,
	}); err != nil {
		t.Fatalf("retry model source switch with latest expected source: %v", err)
	}
	assertTarget(originalCredentialID, originalModelID, originalSecret)
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     originalCredentialID,
		CredentialID:         targetCredentialID,
		ModelID:              targetModelID,
		ExpectedCredentialID: originalCredentialID,
		ExpectedModelID:      originalModelID,
	}); err != nil {
		t.Fatalf("restore switched model source after fresh retry: %v", err)
	}
	assertTarget(targetCredentialID, targetModelID, targetSecret)

	if err := s.DeleteCredential(ctx, targetCredentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete runtime target credential: err = %v, want ErrConflict", err)
	}
	if _, err := s.DeleteCredentialModel(ctx, targetCredentialID, targetModelID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete runtime target model: err = %v, want ErrConflict", err)
	}
	if _, err := s.mutateCredentialModels(ctx, targetCredentialID, func([]platform.CredentialModel) ([]platform.CredentialModel, error) {
		return []platform.CredentialModel{}, nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("replace runtime target model catalog: err = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateCredential(ctx, targetCredentialID, platform.CredentialInput{
		ID: targetCredentialID, Name: "Disabled runtime target", ProviderID: "anthropic",
		Protocol: "anthropic", Endpoint: "https://target.example.test/v1", Enabled: false,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable runtime target credential: err = %v, want ErrConflict", err)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec || jsonb_build_object(
	  'status', 'stopped'
	) WHERE id = $1`, sandboxID); err != nil {
		t.Fatalf("stop model source sandbox: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, originalCredentialID, token); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("stopped sandbox token error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     originalCredentialID,
		CredentialID:         targetCredentialID,
		ModelID:              targetModelID,
		ExpectedCredentialID: targetCredentialID,
		ExpectedModelID:      targetModelID,
	}); !platform.IsValidationError(err) {
		t.Fatalf("stopped sandbox switch error = %v, want validation error", err)
	}
	if err := s.DeleteCredential(ctx, targetCredentialID); err != nil {
		t.Fatalf("stopped sandbox retained temporary target reference: %v", err)
	}
}

func TestLegacySandboxModelSourceSwitchKeepsOtherSignedSlots(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	if _, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s))); err != nil {
		t.Fatalf("register legacy model source Worker: %v", err)
	}
	historyJobID := uuid.NewString()
	firstCredentialID := "legacy-first-" + uuid.NewString()
	secondCredentialID := "legacy-second-" + uuid.NewString()
	targetCredentialID := "legacy-target-" + uuid.NewString()
	firstModelID := "legacy-first-model"
	secondModelID := "legacy-second-model"
	targetModelID := "legacy-target-model"
	insertRuntimeModelSourceCredential(
		t, s, firstCredentialID, "openai", "openai-chat", "https://first.example.test/v1", firstModelID, "first-secret",
	)
	insertRuntimeModelSourceCredential(
		t, s, secondCredentialID, "openai", "openai-chat", "https://second.example.test/v1", secondModelID, "second-secret",
	)
	insertRuntimeModelSourceCredential(
		t, s, targetCredentialID, "anthropic", "anthropic", "https://target.example.test/v1", targetModelID, "target-secret",
	)
	sandboxID := "sandbox-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at,
	   generation, observed_generation)
	  VALUES ($1, 'sandbox', 'default', 'Legacy model source sandbox', '', TRUE,
	    jsonb_build_object(
	      'status', 'running',
	      'credentialIds', jsonb_build_array($2::text),
	      'modelBindings', jsonb_build_object($2::text, $3::text)
	    ), NOW(), NOW(), 3, 2)`, sandboxID, firstCredentialID, firstModelID); err != nil {
		t.Fatalf("insert legacy model source sandbox: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, resource_generation,
	   attempts, created_at, updated_at)
	  VALUES ($1, $2, $3, 'start-sandbox', 'succeeded', jsonb_build_object(
	    'credentialIds', jsonb_build_array($4::text, $5::text),
	    'modelBindings', jsonb_build_object(
	      $4::text, $6::text,
	      $5::text, $7::text
	    )
	  ), 2, 1, NOW() - INTERVAL '1 minute', NOW() - INTERVAL '1 minute')`,
		historyJobID, serverID, sandboxID, firstCredentialID, secondCredentialID, firstModelID, secondModelID); err != nil {
		t.Fatalf("insert legacy applied model source job: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM audit_outbox WHERE entry->>'resourceId' = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM system_logs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE id = $1", historyJobID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM provider_credentials WHERE id = ANY($1)", []string{
			firstCredentialID, secondCredentialID, targetCredentialID,
		})
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM server_pairings WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	secondToken, err := s.issueRuntimeLLMToken(
		sandboxID, secondCredentialID, secondModelID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue legacy second-slot token: %v", err)
	}
	if err := s.DeleteCredential(ctx, secondCredentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete legacy applied credential: err = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     firstCredentialID,
		CredentialID:         targetCredentialID,
		ModelID:              targetModelID,
		ExpectedCredentialID: firstCredentialID,
		ExpectedModelID:      firstModelID,
	}); err != nil {
		t.Fatalf("switch legacy first slot: %v", err)
	}

	sandbox, err := s.GetResource(ctx, sandboxID)
	if err != nil {
		t.Fatalf("load switched legacy sandbox: %v", err)
	}
	sources, ok := sandbox.Spec["runtimeModelSources"].(map[string]any)
	if !ok || len(sources) != 2 {
		t.Fatalf("promoted legacy runtime sources = %#v", sandbox.Spec["runtimeModelSources"])
	}
	if !sandboxHasRuntimeModelSourceSnapshot(sandbox.Spec) {
		t.Fatalf("legacy applied sources were not promoted to a complete snapshot: %#v", sandbox.Spec)
	}
	firstSource, ok := sandboxRuntimeModelSource(sandbox.Spec, firstCredentialID)
	if !ok || firstSource.CredentialID != targetCredentialID || firstSource.ModelID != targetModelID {
		t.Fatalf("promoted first source = %#v, present=%v", firstSource, ok)
	}
	secondSource, ok := sandboxRuntimeModelSource(sandbox.Spec, secondCredentialID)
	if !ok || secondSource.CredentialID != secondCredentialID || secondSource.ModelID != secondModelID {
		t.Fatalf("promoted second source = %#v, present=%v", secondSource, ok)
	}
	target, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, secondCredentialID, secondToken)
	if err != nil {
		t.Fatalf("resolve unchanged legacy second slot: %v", err)
	}
	if target.CredentialID != secondCredentialID || target.ModelID != secondModelID || target.Secret != "second-secret" {
		t.Fatalf("legacy second slot silently changed = %#v", target)
	}
}

func TestUnknownLegacySandboxBlocksSourceSwitchAndCredentialRemoval(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	credentialID := "unknown-legacy-target-" + uuid.NewString()
	modelID := "unknown-legacy-model"
	insertRuntimeModelSourceCredential(
		t, s, credentialID, "openai", "openai-chat", "https://unknown.example.test/v1", modelID, "unknown-secret",
	)
	sandboxID := "sandbox-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at,
	   generation, observed_generation)
	  VALUES ($1, 'sandbox', 'default', 'Unknown legacy model source sandbox', '', TRUE,
	    jsonb_build_object(
	      'status', 'running',
	      'credentialIds', '[]'::jsonb,
	      'modelBindings', '{}'::jsonb
	    ), NOW(), NOW(), 8, 7)`, sandboxID); err != nil {
		t.Fatalf("insert unknown legacy sandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM audit_outbox WHERE entry->>'resourceId' = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM system_logs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM provider_credentials WHERE id = $1", credentialID)
	})

	if _, err := s.UpdateSandboxModelSource(ctx, sandboxID, platform.SandboxModelSourceInput{
		SlotCredentialID:     credentialID,
		CredentialID:         credentialID,
		ModelID:              modelID,
		ExpectedCredentialID: credentialID,
		ExpectedModelID:      modelID,
	}); !platform.IsValidationError(err) {
		t.Fatalf("unknown legacy source switch error = %v, want validation error", err)
	}
	if err := s.DeleteCredential(ctx, credentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown legacy credential delete error = %v, want ErrConflict", err)
	}
	if _, err := s.DeleteCredentialModel(ctx, credentialID, modelID); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown legacy model delete error = %v, want ErrConflict", err)
	}
}

func TestSuccessfulSandboxRestartSnapshotsAppliedModelSources(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register model source snapshot Worker: %v", err)
	}
	sandboxID := "sandbox-" + uuid.NewString()
	jobID := uuid.NewString()
	stopJobID := uuid.NewString()
	firstSlot := "snapshot-first-" + uuid.NewString()
	secondSlot := "snapshot-second-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at,
	   generation, observed_generation)
	  VALUES ($1, 'sandbox', 'default', 'Runtime model snapshot sandbox', '', TRUE,
	    jsonb_build_object(
	      'status', 'restarting',
	      'runtimeModelSources', jsonb_build_object(
	        $2::text, jsonb_build_object('credentialId', 'temporary-target', 'modelId', 'temporary-model')
	      )
	    ), NOW(), NOW(), 4, 3)`, sandboxID, firstSlot); err != nil {
		t.Fatalf("insert model source snapshot sandbox: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, resource_generation,
	   lease_until, attempts, created_at, updated_at)
	  VALUES ($1, $2, $3, 'restart-sandbox', 'leased', jsonb_build_object(
	    'credentialIds', jsonb_build_array($4::text, $5::text),
	    'modelBindings', jsonb_build_object($4::text, 'first-model', $5::text, 'second-model'),
	    'desktop', FALSE, 'proxyId', '', 'runtimeTokenEpoch', $1::text
	  ), 4, NOW() + INTERVAL '10 minutes', 1, NOW(), NOW())`,
		jobID, serverID, sandboxID, firstSlot, secondSlot); err != nil {
		t.Fatalf("insert model source snapshot job: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM audit_outbox WHERE entry->>'resourceId' = ANY($1)", []string{sandboxID, jobID, stopJobID})
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM system_logs WHERE resource_id = ANY($1)", []string{sandboxID, jobID, stopJobID})
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM server_pairings WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	if err := s.CompleteWorkerJob(ctx, serverID, workerCredential, jobID, platform.WorkerJobResult{
		LeaseGeneration: 1, Success: true, ExternalID: "snapshot-instance",
	}); err != nil {
		t.Fatalf("complete model source snapshot restart: %v", err)
	}
	sandbox, err := s.GetResource(ctx, sandboxID)
	if err != nil {
		t.Fatalf("load model source snapshot sandbox: %v", err)
	}
	if sandbox.Generation != 4 || sandbox.ObservedGeneration != 4 || sandbox.Spec["status"] != "running" {
		t.Fatalf("completed sandbox state = %#v", sandbox)
	}
	sources, ok := sandbox.Spec["runtimeModelSources"].(map[string]any)
	if !ok || len(sources) != 2 {
		t.Fatalf("runtime model source snapshot = %#v", sandbox.Spec["runtimeModelSources"])
	}
	if !sandboxHasRuntimeModelSourceSnapshot(sandbox.Spec) {
		t.Fatalf("completed restart did not mark the runtime model source snapshot complete: %#v", sandbox.Spec)
	}
	if sandbox.Spec["runtimeModelTokenEpoch"] != jobID {
		t.Fatalf("completed restart token epoch = %#v, want %q", sandbox.Spec["runtimeModelTokenEpoch"], jobID)
	}
	for slotID, modelID := range map[string]string{firstSlot: "first-model", secondSlot: "second-model"} {
		source, ok := sandboxRuntimeModelSource(sandbox.Spec, slotID)
		if !ok || source.CredentialID != slotID || source.ModelID != modelID || source.UpdatedAt.IsZero() {
			t.Fatalf("snapshot source for %s = %#v, present=%v", slotID, source, ok)
		}
	}

	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, resource_generation,
	   lease_until, attempts, created_at, updated_at)
	  VALUES ($1, $2, $3, 'stop-sandbox', 'leased', '{}'::jsonb, 4,
	    NOW() + INTERVAL '10 minutes', 1, NOW(), NOW())`, stopJobID, serverID, sandboxID); err != nil {
		t.Fatalf("insert model source stop job: %v", err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, workerCredential, stopJobID, platform.WorkerJobResult{
		LeaseGeneration: 1, Success: true, ExternalID: "snapshot-instance",
	}); err != nil {
		t.Fatalf("complete model source stop: %v", err)
	}
	stopped, err := s.GetResource(ctx, sandboxID)
	if err != nil {
		t.Fatalf("load stopped model source sandbox: %v", err)
	}
	if stopped.Spec["status"] != "stopped" {
		t.Fatalf("stopped sandbox status = %#v", stopped.Spec["status"])
	}
	if _, exists := stopped.Spec["runtimeModelSources"]; exists {
		t.Fatalf("stopped sandbox retained runtime model sources: %#v", stopped.Spec)
	}
	if _, exists := stopped.Spec["runtimeModelSourcesComplete"]; exists {
		t.Fatalf("stopped sandbox retained runtime model source completeness: %#v", stopped.Spec)
	}
	if _, exists := stopped.Spec["runtimeModelTokenEpoch"]; exists {
		t.Fatalf("stopped sandbox retained runtime model token epoch: %#v", stopped.Spec)
	}
}

func TestPendingLifecycleTokenWorksOnlyForItsActiveLease(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register pending-token Worker: %v", err)
	}
	sandboxID := "sandbox-" + uuid.NewString()
	oldCredentialID := "pending-old-" + uuid.NewString()
	newCredentialID := "pending-new-" + uuid.NewString()
	oldModelID := "old-model"
	newModelID := "new-model"
	oldEpoch := uuid.NewString()
	jobID := uuid.NewString()
	insertRuntimeModelSourceCredential(
		t, s, oldCredentialID, "openai", "openai-chat", "https://old.example.test/v1", oldModelID, "old-secret",
	)
	insertRuntimeModelSourceCredential(
		t, s, newCredentialID, "anthropic", "anthropic", "https://new.example.test/v1", newModelID, "new-secret",
	)
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at,
	   generation, observed_generation)
	  VALUES ($1, 'sandbox', 'default', 'Pending token sandbox', '', TRUE,
	    jsonb_build_object(
	      'status', 'restarting',
	      'runtimeModelSources', jsonb_build_object(
	        $2::text, jsonb_build_object('credentialId', $2::text, 'modelId', $3::text)
	      ),
	      'runtimeModelSourcesComplete', TRUE,
	      'runtimeModelTokenEpoch', $4::text
	    ), NOW(), NOW(), 2, 1)`, sandboxID, oldCredentialID, oldModelID, oldEpoch); err != nil {
		t.Fatalf("insert pending-token sandbox: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, resource_generation,
	   lease_until, attempts, created_at, updated_at)
	  VALUES ($1, $2, $3, 'restart-sandbox', 'leased', jsonb_build_object(
	    'credentialIds', jsonb_build_array($4::text),
	    'modelBindings', jsonb_build_object($4::text, $5::text),
	    'runtimeTokenEpoch', $1::text
	  ), 2, NOW() + INTERVAL '10 minutes', 1, NOW(), NOW())`,
		jobID, serverID, sandboxID, newCredentialID, newModelID); err != nil {
		t.Fatalf("insert pending-token job: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE resource_id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM provider_credentials WHERE id = ANY($1)", []string{oldCredentialID, newCredentialID})
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM server_pairings WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	pendingToken, err := s.issueRuntimeLLMTokenForEpoch(
		sandboxID, newCredentialID, newModelID, jobID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue pending lifecycle token: %v", err)
	}
	target, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, newCredentialID, pendingToken)
	if err != nil {
		t.Fatalf("resolve active pending lifecycle token: %v", err)
	}
	if target.CredentialID != newCredentialID || target.ModelID != newModelID || target.Secret != "new-secret" {
		t.Fatalf("pending lifecycle target = %#v", target)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET cancel_requested_at = NOW() WHERE id = $1`, jobID); err != nil {
		t.Fatalf("request pending lifecycle cancellation: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, newCredentialID, pendingToken); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("cancelled pending token error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET lease_until = NOW() + INTERVAL '20 minutes' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("renew cancelled pending lifecycle lease: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, newCredentialID, pendingToken); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("renewed cancelled pending token error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET cancel_requested_at = NULL WHERE id = $1`, jobID); err != nil {
		t.Fatalf("clear pending lifecycle cancellation: %v", err)
	}

	oldToken, err := s.issueRuntimeLLMTokenForEpoch(
		sandboxID, oldCredentialID, oldModelID, oldEpoch, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issue old observed token: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, oldCredentialID, oldToken); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("old token during restart error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET status = 'failed', lease_until = NULL WHERE id = $1`, jobID); err != nil {
		t.Fatalf("fail pending lifecycle: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET
	  spec = spec || jsonb_build_object('status', 'running') WHERE id = $1`, sandboxID); err != nil {
		t.Fatalf("restore sandbox after failed lifecycle: %v", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, newCredentialID, pendingToken); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("failed pending token error = %v, want ErrRuntimeUnauthorized", err)
	}
	if _, err := s.ResolveRuntimeLLMTarget(ctx, sandboxID, oldCredentialID, oldToken); err != nil {
		t.Fatalf("failed lifecycle rotated previous epoch: %v", err)
	}
}
