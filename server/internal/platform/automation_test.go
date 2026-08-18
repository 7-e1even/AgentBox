package platform

import (
	"strings"
	"testing"
)

func validAutomationInput() AutomationInput {
	return AutomationInput{
		ProjectID:         "default",
		Name:              "PR preview",
		Enabled:           true,
		ConditionTemplate: "true",
		Trigger: AutomationTriggerInput{
			Type: "webhook", AuthMode: AutomationAuthBearer,
		},
		Action: AutomationActionInput{
			Type: "create-sandbox", TemplateID: "runtime-one", InputTemplate: `{}`,
			CleanupPolicy: AutomationCleanupNever,
		},
	}
}

func TestValidateAutomationInputAcceptsWebhookSandboxAction(t *testing.T) {
	if err := ValidateAutomationInput(validAutomationInput()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAutomationInputRejectsUnknownTriggerAndOversizedTemplate(t *testing.T) {
	input := validAutomationInput()
	input.Trigger.Type = "cron"
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected unknown trigger to fail")
	}
	input = validAutomationInput()
	input.Action.InputTemplate = strings.Repeat("x", (64<<10)+1)
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected oversized input template to fail")
	}
}

func TestValidateAutomationInputAcceptsPipelineAuthModes(t *testing.T) {
	for _, mode := range []AutomationAuthMode{
		AutomationAuthBearer,
		AutomationAuthHMAC,
		AutomationAuthGitHub,
		AutomationAuthGitLab,
		AutomationAuthStandardWebhook,
	} {
		input := validAutomationInput()
		input.Trigger.AuthMode = mode
		NormalizeAutomationInput(&input)
		if err := ValidateAutomationInput(input); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
}

func TestValidateAutomationInputAcceptsRunAndDestroyActions(t *testing.T) {
	run := validAutomationInput()
	run.Action.Type = "run-task"
	run.Action.CommandTemplate = `go test ./{{ .payload.package }}`
	NormalizeAutomationInput(&run)
	if run.Action.TimeoutSeconds != 900 || run.Action.CleanupPolicy != AutomationCleanupNever {
		t.Fatalf("unexpected defaults: %#v", run.Action)
	}
	if err := ValidateAutomationInput(run); err != nil {
		t.Fatal(err)
	}

	destroy := validAutomationInput()
	destroy.Action = AutomationActionInput{Type: "destroy-sandbox", TargetTemplate: `{{ .payload.sandboxId }}`}
	NormalizeAutomationInput(&destroy)
	if err := ValidateAutomationInput(destroy); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAutomationInputRejectsUnsafeActionBounds(t *testing.T) {
	run := validAutomationInput()
	run.Action.Type = "run-task"
	run.Action.CommandTemplate = "make test"
	run.Action.TimeoutSeconds = 5
	NormalizeAutomationInput(&run)
	if err := ValidateAutomationInput(run); err == nil {
		t.Fatal("expected short timeout to fail")
	}

	input := validAutomationInput()
	input.Secret = "too-short"
	NormalizeAutomationInput(&input)
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected short custom secret to fail")
	}
}
