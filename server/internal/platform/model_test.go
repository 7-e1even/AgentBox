package platform

import (
	"strings"
	"testing"
)

func TestValidateControlPlaneResource(t *testing.T) {
	projectID := "default"
	valid := Input{ID: "custom-api-token", Kind: KindVariable, ProjectID: &projectID, Name: "Custom API Token", Enabled: true, Spec: map[string]any{"key": "CUSTOM_API_TOKEN", "reference": "env://CUSTOM_API_TOKEN"}}
	if err := Validate(valid); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	valid.Spec["reference"] = ""
	if err := Validate(valid); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want validation error", err)
	}
}

func TestValidateServerRegistration(t *testing.T) {
	input := ServerRegistration{
		PairingToken: "12345678901234567890123456789012",
		ServerID:     "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
		Name:         "compute-01",
		Hostname:     "compute-01",
		OS:           "linux",
		Arch:         "amd64",
		Capabilities: []string{"docker", "kvm"},
	}
	if err := ValidateServerRegistration(input); err != nil {
		t.Fatalf("ValidateServerRegistration() returned error: %v", err)
	}
	input.ServerID = "not-a-uuid"
	if err := ValidateServerRegistration(input); !IsValidationError(err) {
		t.Fatalf("ValidateServerRegistration() error = %v, want validation error", err)
	}
}

func TestValidateServerInventoryRejectsOversizedValues(t *testing.T) {
	inventory := ServerInventory{
		DockerImages: []ServerImage{{Reference: strings.Repeat("x", 1001)}},
	}
	if err := ValidateServerInventory(inventory); !IsValidationError(err) {
		t.Fatalf("ValidateServerInventory() error = %v, want validation error", err)
	}
}

func TestEnvironmentTemplateRequiresServerInventorySelection(t *testing.T) {
	projectID := "default"
	input := Input{
		ID:        "runtime-one",
		Kind:      KindRuntime,
		ProjectID: &projectID,
		Name:      "Runtime One",
		Spec: map[string]any{
			"driver":         "docker",
			"imageReference": "ubuntu:24.04",
		},
	}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want missing server error", err)
	}
	input.Spec["serverId"] = "7b20f83b-6418-4a9f-8477-3dc7c35d6310"
	input.Spec["agentTools"] = []any{
		"antigravity", "claude-code", "codebuddy", "codex", "copilot-cli",
		"cursor", "gemini-cli", "grok", "kimi", "omp",
		"openclaw", "opencode", "pi", "qoder-cli", "qoder-cn", "qwen-code",
		"qwenpaw", "reasonix",
	}
	if err := Validate(input); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	input.Spec["agentTools"] = []any{"unknown-agent"}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want unsupported Agent tool rejection", err)
	}
	input.Spec["agentTools"] = []any{"pi"}
	input.Spec["driver"] = "boxlite"
	if err := Validate(input); err != nil {
		t.Fatalf("Validate() rejected BoxLite driver: %v", err)
	}
	input.Spec["driver"] = "microsandbox"
	if err := Validate(input); err != nil {
		t.Fatalf("Validate() rejected Microsandbox driver: %v", err)
	}
	input.Spec["driver"] = "unknown"
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want unknown driver rejection", err)
	}
}

func TestSandboxRequiresTargetServer(t *testing.T) {
	projectID := "default"
	input := Input{
		ID:        "sandbox-one",
		Kind:      KindSandbox,
		ProjectID: &projectID,
		Name:      "Sandbox One",
		Spec: map[string]any{
			"runtimeId":  "runtime-one",
			"agentTools": []any{"kimi", "codex", "pi"},
		},
	}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want missing target server error", err)
	}
	input.Spec["serverId"] = "7b20f83b-6418-4a9f-8477-3dc7c35d6310"
	if err := Validate(input); err != nil {
		t.Fatalf("expected sandbox without Agent binding to be valid, got %v", err)
	}
}

func TestSandboxRejectsUnknownAgentTool(t *testing.T) {
	projectID := "default"
	input := Input{
		ID:        "sandbox-one",
		Kind:      KindSandbox,
		ProjectID: &projectID,
		Name:      "Sandbox One",
		Spec: map[string]any{
			"runtimeId":  "runtime-one",
			"serverId":   "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
			"agentTools": []any{"kimi", "unknown-agent"},
		},
	}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want unknown Agent tool rejection", err)
	}
}

func TestValidateImage(t *testing.T) {
	input := Input{
		ID:      "ubuntu-2404",
		Kind:    KindImage,
		Name:    "Ubuntu 24.04",
		Enabled: true,
		Spec: map[string]any{
			"reference":    "ubuntu:24.04",
			"architecture": "all",
			"modes":        []any{"docker", "vm"},
		},
	}
	if err := Validate(input); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	input.Spec["modes"] = []any{}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want missing mode error", err)
	}
}

func TestValidateCredential(t *testing.T) {
	input := CredentialInput{
		ID:         "openai-main",
		Name:       "OpenAI Main",
		ProviderID: "openai",
		Protocol:   "openai-responses",
		Endpoint:   "https://api.openai.com/v1",
		ModelID:    "gpt-5.3-codex",
		Secret:     "sk-example",
		Enabled:    true,
	}
	if err := ValidateCredential(input, true); err != nil {
		t.Fatalf("ValidateCredential() returned error: %v", err)
	}
	input.Endpoint = "file:///etc/passwd"
	if err := ValidateCredential(input, true); !IsValidationError(err) {
		t.Fatalf("ValidateCredential() error = %v, want invalid endpoint", err)
	}
	input.Endpoint = "https://api.openai.com/v1"
	input.ModelID = "legacy-default"
	NormalizeCredential(&input)
	if input.ModelID != "" {
		t.Fatalf("NormalizeCredential() modelId = %q, want no credential default", input.ModelID)
	}
	input.Secret = "line-one\nline-two"
	if err := ValidateCredential(input, true); !IsValidationError(err) {
		t.Fatalf("ValidateCredential() error = %v, want newline rejection", err)
	}
}
