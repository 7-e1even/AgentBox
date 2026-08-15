package platform

import (
	"strings"
	"testing"
)

func validAutomationInput() AutomationInput {
	return AutomationInput{
		ProjectID: "default",
		Name:      "PR preview",
		Enabled:   true,
		Trigger: AutomationTriggerInput{
			Type: "webhook", AuthMode: AutomationAuthBearer,
		},
		Action: AutomationActionInput{
			Type: "create-sandbox", TemplateID: "runtime-one", InputTemplate: `{}`,
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
