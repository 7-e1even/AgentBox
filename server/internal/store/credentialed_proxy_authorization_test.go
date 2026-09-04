package store

import (
	"errors"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestCredentialedProxyActorRequiresAuthenticatedAdmin(t *testing.T) {
	tests := []struct {
		name    string
		actor   platform.AuditActor
		allowed bool
	}{
		{name: "admin", actor: platform.AuditActor{Type: "user", Role: platform.UserRoleAdmin}, allowed: true},
		{name: "operator", actor: platform.AuditActor{Type: "user", Role: platform.UserRoleOperator}},
		{name: "viewer", actor: platform.AuditActor{Type: "user", Role: platform.UserRoleViewer}},
		{name: "webhook", actor: platform.AuditActor{Type: "webhook"}},
		{name: "worker", actor: platform.AuditActor{Type: "worker"}},
		{name: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := platform.WithAuditActor(t.Context(), test.actor)
			if got := credentialedProxyActorAllowed(ctx); got != test.allowed {
				t.Fatalf("credentialedProxyActorAllowed() = %v, want %v", got, test.allowed)
			}
		})
	}
}

func TestLegacyAppliedProxySnapshotRemainsAdminOnly(t *testing.T) {
	s, serverID, credential, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleAdmin,
	})
	operatorCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleOperator,
	})
	proxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "legacy-proxy", Name: "Legacy proxy", Scheme: "http", Host: "proxy.invalid", Port: 8080,
		Username: "legacy", Password: "historical password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["network"] = "egress"
	template.Spec["proxyId"] = proxy.ID
	template, err = s.UpdateResource(adminCtx, template.ID, template.Input)
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := s.CreateResource(adminCtx, extensionSandboxInput(serverID, template))
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "legacy-container",
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a sandbox started by a version that did not persist appliedProxyId,
	// followed by removal of the inherited proxy from its runtime template.
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	  SET spec = spec - 'appliedProxyId' - 'proxyOperation'
	  WHERE id = $1 AND kind = 'sandbox'`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	delete(template.Spec, "proxyId")
	if _, err := s.UpdateResource(adminCtx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	// Retention may remove the only legacy job that used to reveal the applied
	// proxy. A missing snapshot must remain unknown, not become implicitly safe.
	if _, err := s.pool.Exec(ctx, "DELETE FROM worker_jobs WHERE resource_id = $1", sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeSandboxCredentialAccess(operatorCtx, sandbox.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("legacy applied proxy access error = %v, want ErrForbidden", err)
	}
	if err := s.AuthorizeSandboxCredentialAccess(adminCtx, sandbox.ID); err != nil {
		t.Fatalf("admin legacy applied proxy access: %v", err)
	}
}

func TestCredentialedProxyWebhookAutomationFailsClosed(t *testing.T) {
	s, _, _, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminID := uuid.NewString()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: adminID, Role: platform.UserRoleAdmin,
	})
	operatorID := uuid.NewString()
	operatorCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: operatorID, Role: platform.UserRoleOperator,
	})
	proxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "automation-proxy", Name: "Automation proxy", Scheme: "http", Host: "proxy.invalid", Port: 8080,
		Username: "automation", Password: "automation password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["network"] = "egress"
	template.Spec["proxyId"] = proxy.ID
	template, err = s.UpdateResource(adminCtx, template.ID, template.Input)
	if err != nil {
		t.Fatal(err)
	}
	input := platform.AutomationInput{
		ProjectID: "default", Name: "Credentialed proxy automation", Enabled: true,
		Secret: "credentialed-proxy-webhook-secret",
		Trigger: platform.AutomationTriggerInput{
			Type: "webhook", AuthMode: platform.AutomationAuthBearer,
		},
		TemplateID: template.ID,
	}
	if _, _, err := s.CreateAutomation(operatorCtx, input, operatorID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator automation create error = %v, want ErrForbidden", err)
	}
	automation, secret, err := s.CreateAutomation(adminCtx, input, adminID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.TriggerAutomation(ctx, platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret, Body: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.Status != platform.AutomationRunFailed || result.Run.SandboxID != nil {
		t.Fatalf("credentialed proxy webhook result = %#v, want failed without sandbox", result.Run)
	}
}

func TestReferencedProxyCannotBecomeCredentialed(t *testing.T) {
	s, _, _, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleAdmin,
	})
	proxyInput := platform.NetworkProxyInput{
		ID: "credential-transition-proxy", Name: "Credential transition proxy",
		Scheme: "http", Host: "proxy.invalid", Port: 8080, Enabled: true,
	}
	proxy, err := s.CreateNetworkProxy(adminCtx, proxyInput)
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["network"] = "egress"
	template.Spec["proxyId"] = proxy.ID
	if _, err := s.UpdateResource(adminCtx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	proxyInput.Username = "transition-user"
	proxyInput.Password = "new reusable password"
	if _, err := s.UpdateNetworkProxy(adminCtx, proxy.ID, proxyInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("referenced proxy credential transition error = %v, want ErrConflict", err)
	}
}

func TestExistingSandboxCannotBindCredentialedProxy(t *testing.T) {
	s, serverID, credential, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleAdmin,
	})
	input := extensionSandboxInput(serverID, template)
	input.Spec["network"] = "egress"
	sandbox, err := s.CreateResource(adminCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "existing-container",
	}); err != nil {
		t.Fatal(err)
	}
	proxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "late-credentialed-proxy", Name: "Late credentialed proxy", Scheme: "http",
		Host: "proxy.invalid", Port: 8080, Username: "late", Password: "reusable password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateSandboxNetworkProxy(adminCtx, sandbox.ID, proxy.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("late credentialed proxy binding error = %v, want ErrConflict", err)
	}
	var desiredProxyID string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(spec->>'proxyId', '')
	  FROM control_resources WHERE id = $1`, sandbox.ID).Scan(&desiredProxyID); err != nil {
		t.Fatal(err)
	}
	if desiredProxyID != "" {
		t.Fatalf("rejected proxy binding persisted proxyId = %q", desiredProxyID)
	}
	var proxyJobs int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM worker_jobs
	  WHERE resource_id = $1 AND action = 'configure-sandbox-proxy'`, sandbox.ID).Scan(&proxyJobs); err != nil {
		t.Fatal(err)
	}
	if proxyJobs != 0 {
		t.Fatalf("rejected proxy binding enqueued %d worker jobs", proxyJobs)
	}
}

func TestSandboxCreationPersistsManualAndAutomationProxyProvenance(t *testing.T) {
	s, serverID, credential, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminID := uuid.NewString()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: adminID, Role: platform.UserRoleAdmin,
	})
	credentialedProxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "creation-credentialed-proxy", Name: "Creation credentialed proxy", Scheme: "http",
		Host: "proxy.invalid", Port: 8080, Username: "clean", Password: "reusable password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plainProxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "creation-plain-proxy", Name: "Creation plain proxy", Scheme: "http",
		Host: "plain-proxy.invalid", Port: 8080, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["network"] = "egress"
	template.Spec["proxyId"] = credentialedProxy.ID
	template, err = s.UpdateResource(adminCtx, template.ID, template.Input)
	if err != nil {
		t.Fatal(err)
	}
	manualInput := extensionSandboxInput(serverID, template)
	manualInput.ID = "manual-provenance-sandbox"
	manualInput.Spec["extensionIds"] = []string{}
	manual, err := s.CreateResource(adminCtx, manualInput)
	if err != nil {
		t.Fatal(err)
	}
	assertSandboxProxyProvenance(t, s, manual.ID, credentialedProxy.ID, credentialedProxy.ID)
	completeNextSandboxJob(t, s, serverID, credential, "manual-provenance")
	manual, err = s.GetResource(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	manual.Name = "Renamed manual provenance sandbox"
	if _, err := s.UpdateResource(adminCtx, manual.ID, manual.Input); err != nil {
		t.Fatalf("generic sandbox update: %v", err)
	}
	assertSandboxProxyProvenance(t, s, manual.ID, credentialedProxy.ID, credentialedProxy.ID)

	template.Spec["proxyId"] = plainProxy.ID
	template, err = s.UpdateResource(adminCtx, template.ID, template.Input)
	if err != nil {
		t.Fatal(err)
	}
	automation, secret, err := s.CreateAutomation(adminCtx, platform.AutomationInput{
		ProjectID: "default", Name: "Proxy provenance automation", Enabled: true,
		Secret:     "proxy-provenance-automation-secret",
		Trigger:    platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
		TemplateID: template.ID,
	}, adminID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.TriggerAutomation(ctx, platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret, Body: []byte(`{}`),
	})
	if err != nil || result.Run.Status != platform.AutomationRunQueued || result.Run.SandboxID == nil {
		t.Fatalf("automation trigger = %#v, %v", result.Run, err)
	}
	assertSandboxProxyProvenance(t, s, *result.Run.SandboxID, plainProxy.ID, "")
}

func TestSandboxProxySnapshotIgnoresLaterRuntimeCredentialedProxy(t *testing.T) {
	s, serverID, credential, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleAdmin,
	})
	input := extensionSandboxInput(serverID, template)
	input.ID = "frozen-empty-proxy-sandbox"
	input.Spec["extensionIds"] = []string{}
	sandbox, err := s.CreateResource(adminCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	assertSandboxProxyProvenance(t, s, sandbox.ID, "", "")
	completeNextSandboxJob(t, s, serverID, credential, "frozen-empty-proxy")

	proxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "later-runtime-credentialed-proxy", Name: "Later runtime credentialed proxy", Scheme: "http",
		Host: "proxy.invalid", Port: 8080, Username: "later", Password: "later password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["network"] = "egress"
	template.Spec["proxyId"] = proxy.ID
	if _, err := s.UpdateResource(adminCtx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperateSandbox(adminCtx, sandbox.ID, "restart"); err != nil {
		t.Fatalf("restart sandbox with frozen direct proxy: %v", err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if proxyID, _ := job.Payload["proxyId"].(string); proxyID != "" {
		t.Fatalf("restart inherited later runtime proxy %q", proxyID)
	}
	if _, attached := job.Payload["proxy"]; attached {
		t.Fatal("restart attached a later runtime proxy despite the empty creation snapshot")
	}
}

func TestCredentialedProxyProvenanceAllowsCleanRestartAndRejectsLegacyJobs(t *testing.T) {
	s, serverID, credential, _, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	adminCtx := platform.WithAuditActor(ctx, platform.AuditActor{
		Type: "user", ID: uuid.NewString(), Role: platform.UserRoleAdmin,
	})
	proxy, err := s.CreateNetworkProxy(adminCtx, platform.NetworkProxyInput{
		ID: "restart-credentialed-proxy", Name: "Restart credentialed proxy", Scheme: "http",
		Host: "proxy.invalid", Port: 8080, Username: "restart", Password: "restart password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := extensionSandboxInput(serverID, template)
	input.ID = "credentialed-restart-sandbox"
	input.Spec["network"] = "egress"
	input.Spec["proxyId"] = proxy.ID
	input.Spec["extensionIds"] = []string{}
	sandbox, err := s.CreateResource(adminCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	completeNextSandboxJob(t, s, serverID, credential, "credentialed-create")
	if _, err := s.OperateSandbox(adminCtx, sandbox.ID, "restart"); err != nil {
		t.Fatalf("clean credentialed sandbox restart: %v", err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if _, attached := job.Payload["proxy"]; !attached {
		t.Fatal("clean credentialed sandbox restart did not attach its proxy")
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "credentialed-restart",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources
	  SET spec = spec - 'credentialedProxyIdAtCreation' WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperateSandbox(adminCtx, sandbox.ID, "restart"); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy credentialed restart error = %v, want ErrConflict", err)
	}
	if _, err := s.OperateSandboxAgentTools(adminCtx, sandbox.ID, "check", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy credentialed Agent tool error = %v, want ErrConflict", err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyJobID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, created_at, updated_at, resource_generation)
	  VALUES ($1, $2, $3, 'restart-sandbox', 'pending', $4::jsonb, NOW(), NOW(), $5)`,
		legacyJobID, serverID, sandbox.ID, mustMapJSON(map[string]any{
			"sandboxId": sandbox.ID, "driver": "docker", "proxyId": proxy.ID,
		}), sandbox.Generation,
	); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if !errors.Is(err, ErrNoJob) {
		t.Fatalf("legacy queued credentialed job claim error = %v, want ErrNoJob", err)
	}
	if claimed.Payload != nil {
		t.Fatalf("rejected legacy job leaked payload: %#v", claimed.Payload)
	}
	var legacyJobStatus, errorCode string
	if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code
	  FROM worker_jobs WHERE id = $1`, legacyJobID).Scan(&legacyJobStatus, &errorCode); err != nil {
		t.Fatal(err)
	}
	if legacyJobStatus != "failed" || errorCode != "sandbox_proxy_provenance_invalid" {
		t.Fatalf("rejected legacy job = %q/%q, want failed/sandbox_proxy_provenance_invalid", legacyJobStatus, errorCode)
	}
	if _, err := s.ClaimWorkerJob(ctx, serverID, credential); !errors.Is(err, ErrNoJob) {
		t.Fatalf("retired legacy job was claimable again: %v", err)
	}
}

func assertSandboxProxyProvenance(t *testing.T, s *Store, sandboxID, wantProxyID, wantCredentialedProxyID string) {
	t.Helper()
	var proxyID, credentialedProxyID string
	var markerPresent bool
	if err := s.pool.QueryRow(t.Context(), `SELECT COALESCE(spec->>'proxyId', ''),
	  COALESCE(spec->>'credentialedProxyIdAtCreation', ''), spec ? 'credentialedProxyIdAtCreation'
	  FROM control_resources WHERE id = $1 AND kind = 'sandbox'`, sandboxID).Scan(
		&proxyID, &credentialedProxyID, &markerPresent,
	); err != nil {
		t.Fatal(err)
	}
	if proxyID != wantProxyID || credentialedProxyID != wantCredentialedProxyID || !markerPresent {
		t.Fatalf("sandbox %s proxy provenance = %q/%q present=%v, want %q/%q present=true",
			sandboxID, proxyID, credentialedProxyID, markerPresent, wantProxyID, wantCredentialedProxyID)
	}
}

func completeNextSandboxJob(t *testing.T, s *Store, serverID, credential, externalID string) {
	t.Helper()
	job, err := s.ClaimWorkerJob(t.Context(), serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(t.Context(), serverID, credential, job.ID, platform.WorkerJobResult{
		LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: externalID,
	}); err != nil {
		t.Fatal(err)
	}
}
