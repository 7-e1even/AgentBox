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

func assertWaitsForControlPlaneMutationLock(t *testing.T, s *Store, mutation func() error) error {
	t.Helper()
	ctx := t.Context()
	gate, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin control plane mutation gate: %v", err)
	}
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", controlPlaneMutationLockKey); err != nil {
		_ = gate.Rollback(ctx)
		t.Fatalf("lock control plane mutation gate: %v", err)
	}

	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		result <- mutation()
	}()
	<-started

	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-result:
		_ = gate.Rollback(ctx)
		t.Fatalf("mutation completed while advisory lock was held: %v", err)
	case <-timer.C:
	}
	if err := gate.Rollback(ctx); err != nil {
		t.Fatalf("release control plane mutation gate: %v", err)
	}
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not complete after advisory lock was released")
		return nil
	}
}

func insertReferenceTestCredential(t *testing.T, s *Store, id string, modelsJSON string) {
	t.Helper()
	if modelsJSON == "" {
		modelsJSON = "[]"
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO provider_credentials
	  (id, name, provider_id, protocol, endpoint, models, secret_ciphertext,
	   secret_nonce, secret_last_four, enabled, created_at, updated_at)
	  VALUES ($1, 'Reference mutation credential', 'openai', 'openai-chat', '',
	    $2::jsonb, '\\x00'::bytea, '\\x00'::bytea, 'test', TRUE, NOW(), NOW())`,
		id, modelsJSON); err != nil {
		t.Fatalf("insert reference test credential: %v", err)
	}
}

func TestControlPlaneMutationLockSerializesResourceCredentialReferences(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register reference test Worker: %v", err)
	}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, []string{"docker"}, &platform.ServerInventory{}, ""); err != nil {
		t.Fatalf("heartbeat reference test Worker: %v", err)
	}

	firstCredentialID := "credential-" + uuid.NewString()
	secondCredentialID := "credential-" + uuid.NewString()
	insertReferenceTestCredential(t, s, firstCredentialID, "")
	insertReferenceTestCredential(t, s, secondCredentialID, "")
	projectID := "default"
	runtimeID := "runtime-" + uuid.NewString()
	input := platform.Input{
		ID: runtimeID, Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "Reference mutation runtime", Enabled: true,
		Spec: map[string]any{
			"serverId": serverID, "driver": "docker", "imageReference": "agentbox/reference-test:latest",
			"credentialIds": []string{firstCredentialID},
		},
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM control_resources WHERE id = $1", runtimeID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM provider_credentials WHERE id = ANY($1)", []string{firstCredentialID, secondCredentialID})
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.CreateResource(ctx, input)
		return err
	}); err != nil {
		t.Fatalf("create resource after lock release: %v", err)
	}
	if err := s.DeleteCredential(ctx, firstCredentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete bound credential: err = %v, want ErrConflict", err)
	}
	s.catalog = catalog.BuiltinCatalog
	if _, err := s.UpdateCredential(ctx, firstCredentialID, platform.CredentialInput{
		ID: firstCredentialID, Name: "Disabled referenced credential",
		ProviderID: "openai", Protocol: "openai-chat", Enabled: false,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable bound credential: err = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateCredential(ctx, firstCredentialID, platform.CredentialInput{
		ID: firstCredentialID, Name: "Changed referenced credential",
		ProviderID: "anthropic", Protocol: "anthropic", Enabled: true,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("change referenced credential provider: err = %v, want ErrConflict", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.DeleteServer(ctx, serverID)
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete bound managed server after lock release: err = %v, want ErrConflict", err)
	}

	input.Spec["credentialIds"] = []string{firstCredentialID, secondCredentialID}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.UpdateResource(ctx, runtimeID, input)
		return err
	}); err != nil {
		t.Fatalf("update resource after lock release: %v", err)
	}
	if err := s.DeleteCredential(ctx, secondCredentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete credential bound by update: err = %v, want ErrConflict", err)
	}

	if _, err := s.pool.Exec(ctx, "DELETE FROM control_resources WHERE id = $1", runtimeID); err != nil {
		t.Fatalf("remove reference test runtime: %v", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.DeleteCredential(ctx, secondCredentialID)
	}); err != nil {
		t.Fatalf("delete unbound credential after lock release: %v", err)
	}
}

func TestControlPlaneMutationLockSerializesSandboxProxyReferences(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	proxyID := "proxy-" + uuid.NewString()
	if _, err := s.CreateNetworkProxy(ctx, platform.NetworkProxyInput{
		ID: proxyID, Name: "Reference mutation proxy", Scheme: "http",
		Host: "proxy.example.test", Port: 8080, Enabled: true,
	}); err != nil {
		t.Fatalf("create reference test proxy: %v", err)
	}
	sandboxID := "sandbox-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'sandbox', 'default', 'Reference mutation sandbox', '', TRUE,
	    '{"status":"stopped","network":"bridge"}'::jsonb, NOW(), NOW())`, sandboxID); err != nil {
		t.Fatalf("insert reference test sandbox: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM network_proxies WHERE id = $1", proxyID)
	})

	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.UpdateSandboxNetworkProxy(ctx, sandboxID, proxyID)
		return err
	}); err != nil {
		t.Fatalf("bind sandbox proxy after lock release: %v", err)
	}
	if err := s.DeleteNetworkProxy(ctx, proxyID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete bound network proxy: err = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateNetworkProxy(ctx, proxyID, platform.NetworkProxyInput{
		ID: proxyID, Name: "Reference mutation proxy", Scheme: "http",
		Host: "proxy.example.test", Port: 8080, Enabled: false,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable bound network proxy: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec || jsonb_build_object(
	  'proxyId', ''::text, 'appliedProxyId', $1::text
	) WHERE id = $2`, proxyID, sandboxID); err != nil {
		t.Fatalf("retain only applied sandbox proxy reference: %v", err)
	}
	if err := s.DeleteNetworkProxy(ctx, proxyID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete applied network proxy: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM control_resources WHERE id = $1", sandboxID); err != nil {
		t.Fatalf("remove reference test sandbox: %v", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.DeleteNetworkProxy(ctx, proxyID)
	}); err != nil {
		t.Fatalf("delete unbound network proxy after lock release: %v", err)
	}
}

func TestControlPlaneMutationLockSerializesNetworkProxyChecks(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	proxyID := "proxy-" + uuid.NewString()
	if _, err := s.CreateNetworkProxy(ctx, platform.NetworkProxyInput{
		ID: proxyID, Name: "Concurrent check proxy", Scheme: "http",
		Host: "proxy.example.test", Port: 8080, Enabled: true,
	}); err != nil {
		t.Fatalf("create concurrent check proxy: %v", err)
	}
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register concurrent check Worker: %v", err)
	}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, []string{"docker"}, &platform.ServerInventory{}, ""); err != nil {
		t.Fatalf("heartbeat concurrent check Worker: %v", err)
	}
	var checkID string
	t.Cleanup(func() {
		if checkID != "" {
			_, _ = s.pool.Exec(context.Background(), "DELETE FROM worker_jobs WHERE id = $1", checkID)
		}
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM network_proxies WHERE id = $1", proxyID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		check, err := s.CreateNetworkProxyCheck(ctx, proxyID, serverID, "https://example.test/health")
		checkID = check.ID
		return err
	}); err != nil {
		t.Fatalf("create network proxy check after lock release: %v", err)
	}
	if err := s.DeleteNetworkProxy(ctx, proxyID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete proxy with active check: err = %v, want ErrConflict", err)
	}
}

func TestDeleteResourceRejectsActiveOrTransitionalSandbox(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	if _, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s))); err != nil {
		t.Fatalf("register sandbox deletion Worker: %v", err)
	}
	sandboxID := "sandbox-" + uuid.NewString()
	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'sandbox', 'default', 'Deletion guard sandbox', '', TRUE,
	    '{"status":"stopped"}'::jsonb, NOW(), NOW())`, sandboxID); err != nil {
		t.Fatalf("insert deletion guard sandbox: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, lease_until, attempts, created_at, updated_at)
	  VALUES ($1, $2, $3, 'start-sandbox', 'leased', '{}'::jsonb,
	    NOW() + INTERVAL '10 minutes', 1, NOW(), NOW())`, jobID, serverID, sandboxID); err != nil {
		t.Fatalf("insert active sandbox operation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	if err := s.DeleteResource(ctx, sandboxID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete sandbox with active lease: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs
	  SET status = 'failed', lease_until = NULL WHERE id = $1`, jobID); err != nil {
		t.Fatalf("finish active sandbox operation: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	  SET spec = spec || '{"status":"starting"}'::jsonb WHERE id = $1`, sandboxID); err != nil {
		t.Fatalf("prepare transitional sandbox: %v", err)
	}
	if err := s.DeleteResource(ctx, sandboxID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete transitional sandbox: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	  SET spec = spec || '{"status":"error"}'::jsonb WHERE id = $1`, sandboxID); err != nil {
		t.Fatalf("mark sandbox safe to remove: %v", err)
	}
	if err := s.DeleteResource(ctx, sandboxID); err != nil {
		t.Fatalf("delete inactive failed sandbox: %v", err)
	}
}

func TestControlPlaneMutationLockSerializesSandboxOperations(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	sandboxID := "sandbox-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'sandbox', 'default', 'Mutation lock sandbox', '', TRUE,
	    '{"status":"stopped"}'::jsonb, NOW(), NOW())`, sandboxID); err != nil {
		t.Fatalf("insert mutation lock sandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
	})

	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.OperateSandbox(ctx, sandboxID, "stop")
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stop inactive sandbox after lock release: err = %v, want ErrConflict", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.OperateSandboxAgentTools(ctx, sandboxID, "check", nil)
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("check tools on inactive sandbox after lock release: err = %v, want ErrConflict", err)
	}
}

func TestControlPlaneMutationLockSerializesWorkerUpdateAndServerDeletion(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, workerCredential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatalf("register Worker update server: %v", err)
	}
	var lastJobActivityAt time.Time
	if err := s.pool.QueryRow(ctx, `SELECT last_job_activity_at
	  FROM managed_servers WHERE id = $1`, serverID).Scan(&lastJobActivityAt); err != nil {
		t.Fatalf("load registered Worker job activity: %v", err)
	}
	if age := time.Since(lastJobActivityAt); age < -time.Minute || age > time.Minute {
		t.Fatalf("registered Worker job activity age = %s, want within one minute", age)
	}
	if err := s.HeartbeatServer(ctx, serverID, workerCredential, nil, nil, "v0.1.0"); err != nil {
		t.Fatalf("heartbeat Worker update server: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM server_pairings WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.EnqueueWorkerUpdate(ctx, serverID, "v0.2.0")
	}); err != nil {
		t.Fatalf("enqueue Worker update after lock release: %v", err)
	}
	var jobID string
	if err := s.pool.QueryRow(ctx, `SELECT id FROM worker_jobs
	  WHERE server_id = $1 AND action = 'update-worker' AND status = 'pending'`, serverID).Scan(&jobID); err != nil {
		t.Fatalf("load pending Worker update: %v", err)
	}
	if err := s.DeleteServer(ctx, serverID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete server with pending Worker update: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET status = 'leased',
	  lease_until = NOW() + INTERVAL '10 minutes', attempts = 1 WHERE id = $1`, jobID); err != nil {
		t.Fatalf("lease Worker update: %v", err)
	}
	if err := s.DeleteServer(ctx, serverID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete server with leased Worker update: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE worker_jobs SET status = 'failed',
	  lease_until = NULL WHERE id = $1`, jobID); err != nil {
		t.Fatalf("finish Worker update: %v", err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM server_pairings WHERE server_id = $1", serverID); err != nil {
		t.Fatalf("remove Worker update pairing: %v", err)
	}
	if err := s.DeleteServer(ctx, serverID); err != nil {
		t.Fatalf("delete server after Worker update became terminal: %v", err)
	}
}

func TestActiveWorkerJobPayloadProtectsCredentialModelAndProxyReferences(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	if _, _, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s))); err != nil {
		t.Fatalf("register payload reference Worker: %v", err)
	}
	credentialID := "credential-" + uuid.NewString()
	modelID := "payload-only-model"
	insertReferenceTestCredential(t, s, credentialID,
		`[{"id":"payload-only-model","name":"Payload model","group":"test","source":"manual"}]`)
	proxyID := "proxy-" + uuid.NewString()
	if _, err := s.CreateNetworkProxy(ctx, platform.NetworkProxyInput{
		ID: proxyID, Name: "Payload reference proxy", Scheme: "http",
		Host: "proxy.example.test", Port: 8080, Enabled: true,
	}); err != nil {
		t.Fatalf("create payload reference proxy: %v", err)
	}
	sandboxID := "sandbox-" + uuid.NewString()
	jobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'sandbox', 'default', 'Payload reference sandbox', '', TRUE,
	    jsonb_build_object('status', 'requested', 'serverId', $2::text), NOW(), NOW())`, sandboxID, serverID); err != nil {
		t.Fatalf("insert payload reference sandbox: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, created_at, updated_at)
	  VALUES ($1, $2, $3, 'create-sandbox', 'pending', jsonb_build_object(
	    'credentialIds', jsonb_build_array($4::text),
	    'modelBindings', jsonb_build_object($4::text, $5::text),
	    'proxyId', $6::text
	  ), NOW(), NOW())`, jobID, serverID, sandboxID, credentialID, modelID, proxyID); err != nil {
		t.Fatalf("insert payload-only Worker job references: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM worker_jobs WHERE id = $1", jobID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM control_resources WHERE id = $1", sandboxID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM provider_credentials WHERE id = $1", credentialID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM network_proxies WHERE id = $1", proxyID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM server_pairings WHERE server_id = $1", serverID)
		_, _ = s.pool.Exec(cleanupCtx, "DELETE FROM managed_servers WHERE id = $1", serverID)
	})

	var resourceContainsReferences bool
	if err := s.pool.QueryRow(ctx, `SELECT spec ?| ARRAY[
	  'credentialIds', 'modelBindings', 'proxyId', 'appliedProxyId'
	] FROM control_resources WHERE id = $1`, sandboxID).Scan(&resourceContainsReferences); err != nil {
		t.Fatalf("inspect payload reference sandbox: %v", err)
	}
	if resourceContainsReferences {
		t.Fatal("sandbox spec unexpectedly contains credential, model, or proxy references")
	}

	s.catalog = catalog.BuiltinCatalog
	if _, err := s.UpdateCredential(ctx, credentialID, platform.CredentialInput{
		ID: credentialID, Name: "Payload reference credential",
		ProviderID: "openai", Protocol: "openai-chat", Enabled: false,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable credential referenced only by Worker payload: err = %v, want ErrConflict", err)
	}
	if err := s.DeleteCredential(ctx, credentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete credential referenced only by Worker payload: err = %v, want ErrConflict", err)
	}
	if _, err := s.DeleteCredentialModel(ctx, credentialID, modelID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete model referenced only by Worker payload: err = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateNetworkProxy(ctx, proxyID, platform.NetworkProxyInput{
		ID: proxyID, Name: "Payload reference proxy", Scheme: "http",
		Host: "proxy.example.test", Port: 8080, Enabled: false,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable proxy referenced only by Worker payload: err = %v, want ErrConflict", err)
	}
	if err := s.DeleteNetworkProxy(ctx, proxyID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete proxy referenced only by Worker payload: err = %v, want ErrConflict", err)
	}
}

func TestControlPlaneMutationLockSerializesAutomationReferences(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	credentialID := "credential-" + uuid.NewString()
	modelID := "reference-model"
	insertReferenceTestCredential(t, s, credentialID, `[{"id":"reference-model","name":"Reference model","group":"test","source":"manual"}]`)
	templateID := "runtime-" + uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'runtime', 'default', 'Reference automation template', '', TRUE,
	    jsonb_build_object('driver', 'docker', 'credentialIds', jsonb_build_array($2::text)),
	    NOW(), NOW())`, templateID, credentialID); err != nil {
		t.Fatalf("insert reference automation template: %v", err)
	}
	var automationID string
	var automationEndpointID string
	var automationSecret string
	var automationRunIDs []string
	t.Cleanup(func() {
		for _, runID := range automationRunIDs {
			_, _ = s.pool.Exec(context.Background(), "DELETE FROM worker_jobs WHERE automation_run_id = $1", runID)
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM control_resources
			  WHERE kind = 'sandbox' AND spec->>'automationRunId' = $1`, runID)
			_, _ = s.pool.Exec(context.Background(), "DELETE FROM automation_runs WHERE id = $1", runID)
		}
		if automationID != "" {
			_, _ = s.pool.Exec(context.Background(), "DELETE FROM automations WHERE id = $1", automationID)
		}
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM control_resources WHERE id = $1", templateID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM provider_credentials WHERE id = $1", credentialID)
	})
	input := platform.AutomationInput{
		ProjectID: "default", Name: "Reference mutation automation", Enabled: true,
		Trigger:       platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
		TemplateID:    templateID,
		ModelBindings: map[string]string{credentialID: modelID},
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		automation, secret, err := s.CreateAutomation(ctx, input, uuid.NewString())
		automationID = automation.ID
		automationEndpointID = automation.EndpointID
		automationSecret = secret
		return err
	}); err != nil {
		t.Fatalf("create automation after lock release: %v", err)
	}
	input.Name = "Updated reference automation"
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.UpdateAutomation(ctx, automationID, input, uuid.NewString())
		return err
	}); err != nil {
		t.Fatalf("update automation after lock release: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET enabled = FALSE
	  WHERE id = $1`, templateID); err != nil {
		t.Fatalf("disable automation template for trigger test: %v", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		result, err := s.TestAutomation(ctx, automationID, []byte(`{"source":"manual"}`))
		if err == nil {
			automationRunIDs = append(automationRunIDs, result.Run.ID)
		}
		return err
	}); err != nil {
		t.Fatalf("test automation after lock release: %v", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		result, err := s.TriggerAutomation(ctx, platform.AutomationDelivery{
			EndpointID: automationEndpointID, Authorization: "Bearer " + automationSecret,
			IdempotencyKey: "reference-trigger-" + uuid.NewString(),
			Body:           []byte(`{"source":"webhook"}`),
		})
		if err == nil {
			automationRunIDs = append(automationRunIDs, result.Run.ID)
		}
		return err
	}); err != nil {
		t.Fatalf("trigger automation after lock release: %v", err)
	}
	if len(automationRunIDs) != 2 {
		t.Fatalf("automation run count = %d, want 2", len(automationRunIDs))
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		_, err := s.DeleteCredentialModel(ctx, credentialID, modelID)
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete model bound by automation after lock release: err = %v, want ErrConflict", err)
	}
	if _, err := s.mutateCredentialModels(ctx, credentialID, func([]platform.CredentialModel) ([]platform.CredentialModel, error) {
		return []platform.CredentialModel{}, nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("refresh away model bound by automation: err = %v, want ErrConflict", err)
	}
	if err := s.DeleteResource(ctx, templateID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete automation template: err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	  SET spec = spec || '{"credentialIds":[]}'::jsonb WHERE id = $1`, templateID); err != nil {
		t.Fatalf("remove template credential binding: %v", err)
	}
	if err := s.DeleteCredential(ctx, credentialID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete automation credential: err = %v, want ErrConflict", err)
	}
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.DeleteAutomation(ctx, automationID)
	}); err != nil {
		t.Fatalf("delete automation after lock release: %v", err)
	}
	automationID = ""
	if err := assertWaitsForControlPlaneMutationLock(t, s, func() error {
		return s.DeleteResource(ctx, templateID)
	}); err != nil {
		t.Fatalf("delete unbound automation template after lock release: %v", err)
	}
}
