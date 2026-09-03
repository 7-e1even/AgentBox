package store

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func newExtensionTestEnvironment(t *testing.T) (*Store, string, string, platform.Resource, platform.Resource) {
	t.Helper()
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	serverID := uuid.NewString()
	_, credential, err := s.RegisterServer(ctx, testServerRegistration(serverID, mustCreatePairingToken(t, s)))
	if err != nil {
		t.Fatal(err)
	}
	inventory := platform.ServerInventory{DockerImages: []platform.ServerImage{{Reference: "ubuntu:24.04", Architecture: "amd64"}}}
	if err := s.HeartbeatServer(ctx, serverID, credential, []string{"docker", "sandbox-extensions"}, &inventory, "test"); err != nil {
		t.Fatal(err)
	}
	projectID := "default"
	extension, err := s.CreateResource(ctx, platform.Input{ID: "test-extension", Kind: platform.KindExtension, ProjectID: &projectID,
		Name: "Test extension", Enabled: true, Spec: map[string]any{
			"version": "1.0.0", "installScript": "echo original", "verifyScript": "true", "requiresNetwork": true,
		}})
	if err != nil {
		t.Fatal(err)
	}
	template, err := s.CreateResource(ctx, platform.Input{ID: "test-template", Kind: platform.KindRuntime, ProjectID: &projectID,
		Name: "Test template", Enabled: true, Spec: map[string]any{
			"serverId": serverID, "driver": "docker", "imageReference": "ubuntu:24.04", "extensionIds": []string{extension.ID},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return s, serverID, credential, extension, template
}

func extensionSandboxInput(serverID string, template platform.Resource) platform.Input {
	return platform.Input{ID: "test-sandbox", Kind: platform.KindSandbox, ProjectID: template.ProjectID, Name: "Test sandbox", Enabled: true,
		Spec: map[string]any{"serverId": serverID, "runtimeId": template.ID}}
}

func TestExtensionCreationFreezesDefinitionsAndPreservesInstallationHistory(t *testing.T) {
	s, serverID, credential, extension, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	proxy, err := s.CreateNetworkProxy(ctx, platform.NetworkProxyInput{
		ID: "test-proxy", Name: "Test proxy", Scheme: "http", Host: "proxy.invalid", Port: 8080,
		Username: "installer", Password: "special/@ password", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := extensionSandboxInput(serverID, template)
	input.Spec["proxyId"] = proxy.ID
	input.Spec["extensionSnapshots"] = []any{map[string]any{"id": "forged"}}
	sandbox, err := s.CreateResource(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteResource(ctx, extension.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete referenced extension = %v", err)
	}
	extension.Spec["version"] = "2.0.0"
	extension.Spec["installScript"] = "echo changed"
	if _, err := s.UpdateResource(ctx, extension.ID, extension.Input); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(job.Payload["extensions"])
	var definitions []platform.ExtensionDefinition
	if err := json.Unmarshal(encoded, &definitions); err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].ID != extension.ID || definitions[0].Generation != 1 || definitions[0].Spec.Version != "1.0.0" || definitions[0].Spec.InstallScript != "echo original" {
		t.Fatalf("creation did not freeze definition: %s", encoded)
	}
	redactionValues := job.Payload["proxy"].(map[string]any)["redactionValues"].([]string)
	if len(redactionValues) != 2 || redactionValues[1] != "special/@ password" {
		t.Fatal("claim did not attach raw proxy redaction values")
	}
	var persistedPayload string
	if err := s.pool.QueryRow(ctx, `SELECT payload::text FROM worker_jobs WHERE id = $1`, job.ID).Scan(&persistedPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persistedPayload, "redactionValues") || strings.Contains(persistedPayload, "special/@ password") {
		t.Fatal("claim-only proxy redaction secrets were persisted")
	}
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, job.ID, platform.WorkerJobProgressInput{
		LeaseGeneration: job.LeaseGeneration, Stage: "extensions", ExtensionID: extension.ID, ExtensionStatus: "succeeded", ExtensionOutput: strings.Repeat("x", 4096),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "instance"}); err != nil {
		t.Fatal(err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeStates, _ := json.Marshal(sandbox.Spec["extensionStates"])
	if !strings.Contains(string(beforeStates), `"status":"succeeded"`) {
		t.Fatalf("installation history missing: %s", beforeStates)
	}
	changed := sandbox.Input
	changed.Spec = maps.Clone(sandbox.Spec)
	changed.Spec["extensionIds"] = []string{}
	if _, err := s.UpdateResource(ctx, sandbox.ID, changed); !platform.IsValidationError(err) {
		t.Fatalf("changed creation-only extensions = %v", err)
	}
	changed.Spec = maps.Clone(sandbox.Spec)
	changed.Spec["extensionSnapshots"] = []any{}
	changed.Spec["extensionStates"] = []any{}
	if _, err := s.UpdateResource(ctx, sandbox.ID, changed); err != nil {
		t.Fatal(err)
	}
	template.Spec["extensionIds"] = []string{}
	if _, err := s.UpdateResource(ctx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	extension.Enabled = false
	if _, err := s.UpdateResource(ctx, extension.ID, extension.Input); err != nil {
		t.Fatal(err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	sandbox.Name = "Renamed after extension disabled"
	if _, err := s.UpdateResource(ctx, sandbox.ID, sandbox.Input); err != nil {
		t.Fatalf("existing sandbox should remain editable after extension is disabled: %v", err)
	}
	if _, err := s.OperateSandbox(ctx, sandbox.ID, "restart"); err != nil {
		t.Fatal(err)
	}
	restart, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	restartDefinitions, _ := json.Marshal(restart.Payload["extensions"])
	if string(restartDefinitions) != string(encoded) {
		t.Fatalf("restart changed frozen definitions: %s", restartDefinitions)
	}
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, restart.ID, platform.WorkerJobProgressInput{
		LeaseGeneration: restart.LeaseGeneration, Stage: "extensions", ExtensionID: extension.ID, ExtensionStatus: "installing",
	}); !platform.IsValidationError(err) {
		t.Fatalf("restart accepted extension installation progress: %v", err)
	}
	if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, restart.ID, platform.WorkerJobProgressInput{
		LeaseGeneration: restart.LeaseGeneration, Stage: "verify", Message: "restart verified",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, restart.ID, platform.WorkerJobResult{LeaseGeneration: restart.LeaseGeneration, Success: true}); err != nil {
		t.Fatal(err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterStates, _ := json.Marshal(sandbox.Spec["extensionStates"])
	if string(beforeStates) != string(afterStates) {
		t.Fatal("restart overwrote installation history")
	}
}

func TestLegacySandboxDoesNotInheritNewTemplateExtensions(t *testing.T) {
	s, serverID, credential, sandbox := newVersionedSandbox(t)
	ctx := t.Context()
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{Success: true, ExternalID: "legacy-instance"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE control_resources SET spec = spec - 'extensionIds' - 'extensionSnapshots' WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	projectID := "default"
	extension, err := s.CreateResource(ctx, platform.Input{ID: "later-extension", Kind: platform.KindExtension, ProjectID: &projectID,
		Name: "Later extension", Enabled: true, Spec: map[string]any{"version": "1", "installScript": "true", "verifyScript": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	template, err := s.GetResource(ctx, "version-template")
	if err != nil {
		t.Fatal(err)
	}
	template.Spec["extensionIds"] = []string{extension.ID}
	if _, err := s.UpdateResource(ctx, template.ID, template.Input); err != nil {
		t.Fatal(err)
	}
	sandbox, err = s.GetResource(ctx, sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	sandbox.Spec["extensionIds"] = []string{}
	sandbox, err = s.UpdateResource(ctx, sandbox.ID, sandbox.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := sandbox.Spec["extensionIds"]; exists {
		t.Fatal("editing a legacy sandbox introduced extension IDs")
	}
	if _, err := s.OperateSandbox(ctx, sandbox.ID, "restart"); err != nil {
		t.Fatal(err)
	}
	restart, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	if got := restart.Payload["extensions"].([]any); len(got) != 0 {
		t.Fatalf("legacy restart inherited template extensions: %#v", got)
	}
}

func TestExtensionCreationValidatesReferencesNetworkAndWorker(t *testing.T) {
	for _, scenario := range []string{"disabled", "cross-project", "no-network", "old-worker"} {
		t.Run(scenario, func(t *testing.T) {
			s, serverID, credential, extension, template := newExtensionTestEnvironment(t)
			ctx := t.Context()
			input := extensionSandboxInput(serverID, template)
			switch scenario {
			case "disabled":
				extension.Enabled = false
				if _, err := s.UpdateResource(ctx, extension.ID, extension.Input); err != nil {
					t.Fatal(err)
				}
			case "cross-project":
				project, err := s.CreateResource(ctx, platform.Input{ID: "another-project", Kind: platform.KindProject, Name: "Another project", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				extension.ID = "another-extension"
				extension.ProjectID = &project.ID
				if _, err := s.CreateResource(ctx, extension.Input); err != nil {
					t.Fatal(err)
				}
				input.Spec["extensionIds"] = []string{extension.ID}
			case "no-network":
				input.Spec["network"] = "none"
			case "old-worker":
				if err := s.HeartbeatServer(ctx, serverID, credential, []string{"docker"}, nil, "old"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreateResource(ctx, input); !platform.IsValidationError(err) {
				t.Fatalf("creation should reject %s: %v", scenario, err)
			}
		})
	}
}

func TestExtensionCompletionCannotSucceedWithoutConfirmedVerification(t *testing.T) {
	for _, extensionStatus := range []string{"", "failed", "verifying"} {
		t.Run("status="+extensionStatus, func(t *testing.T) {
			s, serverID, credential, extension, template := newExtensionTestEnvironment(t)
			ctx := t.Context()
			sandbox, err := s.CreateResource(ctx, extensionSandboxInput(serverID, template))
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.ClaimWorkerJob(ctx, serverID, credential)
			if err != nil {
				t.Fatal(err)
			}
			if extensionStatus != "" {
				if _, err := s.ReportWorkerJobProgress(ctx, serverID, credential, job.ID, platform.WorkerJobProgressInput{
					LeaseGeneration: job.LeaseGeneration, Stage: "extensions", ExtensionID: extension.ID, ExtensionStatus: extensionStatus,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{LeaseGeneration: job.LeaseGeneration, Success: true, ExternalID: "unverified-instance"}); err != nil {
				t.Fatal(err)
			}
			sandbox, err = s.GetResource(ctx, sandbox.ID)
			if err != nil {
				t.Fatal(err)
			}
			if sandbox.Spec["status"] != "error" || sandbox.ObservedGeneration != 0 {
				t.Fatalf("unverified sandbox became ready: %#v", sandbox)
			}
			var status, code string
			if err := s.pool.QueryRow(ctx, `SELECT status, result_error_code FROM worker_jobs WHERE id = $1`, job.ID).Scan(&status, &code); err != nil {
				t.Fatal(err)
			}
			if status != "failed" || code != "sandbox_extension_unverified" {
				t.Fatalf("completion = %s/%s", status, code)
			}
		})
	}
}

func TestAutomationCreationFreezesExtensionDefinitions(t *testing.T) {
	s, serverID, credential, extension, template := newExtensionTestEnvironment(t)
	ctx := t.Context()
	secret := "extension-automation-secret-12345678"
	automation, _, err := s.CreateAutomation(ctx, platform.AutomationInput{
		ProjectID: "default", Name: "Extension automation", Enabled: true, Secret: secret,
		Trigger: platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer}, TemplateID: template.ID,
	}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.TriggerAutomation(ctx, platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret, IdempotencyKey: "first", Body: []byte(`{}`),
	})
	if err != nil || result.Run.Status != platform.AutomationRunQueued {
		t.Fatalf("trigger = %#v, %v", result.Run, err)
	}
	extension.Spec["installScript"] = "echo changed"
	if _, err := s.UpdateResource(ctx, extension.ID, extension.Input); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimWorkerJob(ctx, serverID, credential)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(job.Payload["extensions"])
	if !strings.Contains(string(encoded), "echo original") || strings.Contains(string(encoded), "echo changed") {
		t.Fatalf("automated job did not freeze extension: %s", encoded)
	}
	if err := s.CompleteWorkerJob(ctx, serverID, credential, job.ID, platform.WorkerJobResult{LeaseGeneration: job.LeaseGeneration, Success: true}); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetPublicAutomationRun(ctx, automation.EndpointID, result.Run.ID, result.StatusToken)
	if err != nil || run.Status != platform.AutomationRunFailed {
		t.Fatalf("automation accepted unverified extension completion: %#v, %v", run, err)
	}
	extension.Enabled = false
	if _, err := s.UpdateResource(ctx, extension.ID, extension.Input); err != nil {
		t.Fatal(err)
	}
	failed, err := s.TriggerAutomation(ctx, platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret, IdempotencyKey: "second", Body: []byte(`{}`),
	})
	if err != nil || failed.Run.Status != platform.AutomationRunFailed || failed.Run.SandboxID != nil {
		t.Fatalf("automation did not reject disabled extension: %#v, %v", failed.Run, err)
	}
}
