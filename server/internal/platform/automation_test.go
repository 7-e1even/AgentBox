package platform

import "testing"

func validAutomationInput() AutomationInput {
	return AutomationInput{
		ProjectID: "default",
		Name:      "PR preview",
		Enabled:   true,
		Trigger: AutomationTriggerInput{
			Type: "webhook", AuthMode: AutomationAuthBearer,
		},
		TemplateID: "runtime-one",
	}
}

func TestValidateAutomationInputAcceptsWebhookSandboxAction(t *testing.T) {
	if err := ValidateAutomationInput(validAutomationInput()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAutomationInputRejectsUnknownTriggerAndMissingTemplate(t *testing.T) {
	input := validAutomationInput()
	input.Trigger.Type = "cron"
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected unknown trigger to fail")
	}
	input = validAutomationInput()
	input.TemplateID = ""
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected missing template to fail")
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

func TestValidateAutomationInputRejectsShortSecrets(t *testing.T) {
	input := validAutomationInput()
	input.Secret = "too-short"
	NormalizeAutomationInput(&input)
	if err := ValidateAutomationInput(input); err == nil {
		t.Fatal("expected short custom secret to fail")
	}
}
