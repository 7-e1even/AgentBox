package store

import (
	"encoding/json"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestExistingSandboxUsesOnlyItsFrozenExtensions(t *testing.T) {
	template := map[string]any{"extensionIds": []string{"new-template-extension"}}
	sandbox := platform.Resource{Input: platform.Input{Spec: map[string]any{}}}
	definitions, err := loadSandboxExtensions(t.Context(), nil, sandbox, template, false)
	if err != nil || len(definitions) != 0 {
		t.Fatalf("legacy sandbox inherited new extensions: %#v, %v", definitions, err)
	}
	sandbox.Spec["extensionSnapshots"] = []platform.ExtensionDefinition{{ID: "original", Generation: 2, Spec: platform.ExtensionSpec{Version: "1.0.0"}}}
	definitions, err = loadSandboxExtensions(t.Context(), nil, sandbox, template, false)
	if err != nil || len(definitions) != 1 || definitions[0].ID != "original" || definitions[0].Spec.Version != "1.0.0" {
		t.Fatalf("frozen definitions = %#v, %v", definitions, err)
	}
}

func TestExtensionProgressRequiresCreateJobMembership(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"extensions": []platform.ExtensionDefinition{{ID: "selected"}}})
	if err != nil {
		t.Fatal(err)
	}
	input := platform.WorkerJobProgressInput{ExtensionID: "selected", ExtensionStatus: "installing"}
	if err := validateExtensionProgressPayload(input, "create-sandbox", payload); err != nil {
		t.Fatal(err)
	}
	if err := validateExtensionProgressPayload(input, "restart-sandbox", payload); err == nil {
		t.Fatal("accepted extension installation during restart")
	}
	input.ExtensionID = "unselected"
	if err := validateExtensionProgressPayload(input, "create-sandbox", payload); err == nil {
		t.Fatal("accepted unselected extension")
	}
}

func TestExtensionProgressKeepsVerificationAndClosesFailure(t *testing.T) {
	started := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	progress := advanceProvisioningProgress(platform.ProvisioningProgress{}, platform.WorkerJobProgressInput{
		Stage: "extensions", ExtensionID: "verified", ExtensionStatus: "installing",
	}, started)
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "extensions", ExtensionID: "verified", ExtensionStatus: "verifying", ExtensionOutput: "[REDACTED]",
	}, started.Add(time.Second))
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "extensions", ExtensionID: "verified", ExtensionStatus: "succeeded",
	}, started.Add(2*time.Second))
	progress = advanceProvisioningProgress(progress, platform.WorkerJobProgressInput{
		Stage: "extensions", ExtensionID: "incomplete", ExtensionStatus: "installing",
	}, started.Add(3*time.Second))
	progress = finishProvisioningProgress(progress, platform.WorkerJobResult{Message: "installation failed"}, started.Add(5*time.Second))
	if len(progress.Extensions) != 2 {
		t.Fatalf("extensions = %#v", progress.Extensions)
	}
	verified, incomplete := progress.Extensions[0], progress.Extensions[1]
	if verified.Status != "succeeded" || verified.Output != "[REDACTED]" || verified.DurationMS != 2000 {
		t.Fatalf("verified extension was changed: %#v", verified)
	}
	if incomplete.Status != "failed" || incomplete.DurationMS != 2000 || incomplete.FinishedAt == nil {
		t.Fatalf("unfinished extension was not closed: %#v", incomplete)
	}
}
