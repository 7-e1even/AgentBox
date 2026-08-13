package agent

import "testing"

func validInput() Input {
	credential := "openai-primary"
	return Input{
		ProjectID:     "default",
		RuntimeID:     "python-venv",
		Name:          "Research Agent",
		Slug:          "research-agent",
		Description:   "A focused assistant.",
		Avatar:        "RA",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		CredentialID:  &credential,
		SystemPrompt:  "You research questions carefully and explain the evidence behind each conclusion.",
		SkillIDs:      []string{"web-research"},
		MCPServerIDs:  []string{"browser"},
		Temperature:   0.4,
		MaxSteps:      12,
		Concurrency:   1,
		SandboxPolicy: "new",
		Status:        StatusDraft,
	}
}

func TestValidateAcceptsCompleteAgent(t *testing.T) {
	if err := Validate(validInput(), BuiltinCatalog); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
}

func TestValidateRejectsProviderModelMismatch(t *testing.T) {
	input := validInput()
	input.ModelID = "claude-sonnet"
	if err := Validate(input, BuiltinCatalog); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want validation error", err)
	}
}

func TestValidateRejectsInvalidProject(t *testing.T) {
	input := validInput()
	input.ProjectID = "Not Valid"
	if err := Validate(input, BuiltinCatalog); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want validation error", err)
	}
}

func TestNormalizeTrimsFieldsAndInitializesLists(t *testing.T) {
	input := validInput()
	input.Name = "  Research Agent  "
	input.SkillIDs = nil
	input.MCPServerIDs = nil
	Normalize(&input)
	if input.Name != "Research Agent" {
		t.Fatalf("Normalize() name = %q", input.Name)
	}
	if input.SkillIDs == nil || input.MCPServerIDs == nil {
		t.Fatal("Normalize() must initialize capability lists")
	}
}
