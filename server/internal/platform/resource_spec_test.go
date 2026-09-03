package platform

import "testing"

func TestResourceSpecContractsRejectWrongShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		kind Kind
		spec map[string]any
	}{
		{"project fields", KindProject, map[string]any{"serverId": "not-a-project-field"}},
		{"image modes", KindImage, map[string]any{"modes": "docker"}},
		{"runtime cpu", KindRuntime, map[string]any{"cpu": 2}},
		{"sandbox binding", KindSandbox, map[string]any{"modelBindings": map[string]any{"credential": 1}}},
		{"skill version", KindSkill, map[string]any{"version": false}},
		{"mcp args", KindMCP, map[string]any{"args": []string{"one"}}},
		{"variable reference", KindVariable, map[string]any{"reference": 1}},
		{"nested environment", KindRuntime, map[string]any{"environmentVariables": []any{map[string]any{"name": "KEY", "value": 1}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeResourceSpec(Input{Kind: test.kind, Spec: test.spec}); !IsValidationError(err) {
				t.Fatalf("error = %v, want invalid spec", err)
			}
		})
	}
}

func TestResourceSpecPreservesLegacyMissingDesktop(t *testing.T) {
	value, err := DecodeResourceSpec(Input{Kind: KindSandbox, Spec: map[string]any{
		"runtimeId": "template", "status": "running", "provisioning": nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec := value.(*SandboxSpec)
	if spec.Desktop != nil || spec.RuntimeID != "template" {
		t.Fatalf("decoded spec = %#v", spec)
	}
}

func TestDesiredSandboxSpecDropsRuntimeModelSources(t *testing.T) {
	desired := DesiredResourceSpec(KindSandbox, map[string]any{
		"runtimeId": "runtime-one",
		"runtimeModelSources": map[string]any{
			"primary": map[string]any{"credentialId": "target", "modelId": "gpt-5.4"},
		},
		"runtimeModelSourcesComplete": true,
		"runtimeModelTokenEpoch":      "restart-job-one",
	})
	if _, ok := desired["runtimeModelSources"]; ok {
		t.Fatal("runtime model source observation leaked into desired configuration")
	}
	if _, ok := desired["runtimeModelSourcesComplete"]; ok {
		t.Fatal("runtime model source completeness marker leaked into desired configuration")
	}
	if _, ok := desired["runtimeModelTokenEpoch"]; ok {
		t.Fatal("runtime model token epoch leaked into desired configuration")
	}
	if desired["runtimeId"] != "runtime-one" {
		t.Fatalf("desired runtimeId = %#v", desired["runtimeId"])
	}
}

func TestResourceSpecVersionAndMalformedEnvironment(t *testing.T) {
	input := Input{ID: "project", Kind: KindProject, Name: "Project", Enabled: true}
	Normalize(&input)
	if input.SpecVersion != 1 || Validate(input) != nil {
		t.Fatal("legacy input must normalize to specVersion 1")
	}
	input.SpecVersion = 2
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("unknown version accepted: %v", err)
	}
	input = Input{Kind: KindRuntime, Spec: map[string]any{"environmentVariables": "not-an-array"}}
	Normalize(&input)
	if _, err := DecodeResourceSpec(input); !IsValidationError(err) {
		t.Fatalf("normalization hid malformed environment: %v", err)
	}
}
