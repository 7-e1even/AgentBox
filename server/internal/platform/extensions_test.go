package platform

import (
	"reflect"
	"strings"
	"testing"
)

func extensionTestInput() Input {
	projectID := "default"
	return Input{ID: "test-extension", Kind: KindExtension, ProjectID: &projectID, Name: "Test extension", Enabled: true,
		Spec: map[string]any{"version": "1.0.0", "installScript": "echo installed", "verifyScript": "test -d /tmp"}}
}

func TestExtensionDraftAndEnabledValidation(t *testing.T) {
	defaults := extensionTestInput()
	Normalize(&defaults)
	if defaults.Spec["requiresNetwork"] != true || defaults.Spec["timeoutSeconds"] != ExtensionDefaultTimeoutSeconds || defaults.Spec["source"] != "custom" {
		t.Fatalf("extension defaults = %#v", defaults.Spec)
	}
	for _, test := range []struct {
		name    string
		change  func(*Input)
		wantErr bool
	}{
		{"defaults", func(*Input) {}, false},
		{"disabled draft", func(input *Input) { input.Enabled = false; input.Spec = map[string]any{} }, false},
		{"enabled draft", func(input *Input) { input.Spec = map[string]any{} }, true},
		{"empty verification", func(input *Input) { input.Spec["verifyScript"] = " \n" }, true},
		{"oversized script", func(input *Input) { input.Spec["installScript"] = strings.Repeat("界", (64<<10)/3+1) }, true},
		{"long version", func(input *Input) { input.Spec["version"] = strings.Repeat("x", 65) }, true},
		{"bad source", func(input *Input) { input.Spec["source"] = "trusted" }, true},
		{"preset label", func(input *Input) { input.Spec["source"] = "preset" }, false},
		{"offline extension", func(input *Input) { input.Spec["requiresNetwork"] = false }, false},
		{"short timeout", func(input *Input) { input.Spec["timeoutSeconds"] = 29 }, true},
		{"long timeout", func(input *Input) { input.Spec["timeoutSeconds"] = 1801 }, true},
		{"fractional timeout", func(input *Input) { input.Spec["timeoutSeconds"] = 30.5 }, true},
		{"malformed draft", func(input *Input) { input.Enabled = false; input.Spec["installScript"] = 1 }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := extensionTestInput()
			test.change(&input)
			Normalize(&input)
			if err := Validate(input); (err != nil) != test.wantErr {
				t.Fatalf("Validate() = %v, want error %v", err, test.wantErr)
			}
		})
	}
}

func TestExtensionSelectionKeepsOrderAndRejectsMalformedIDs(t *testing.T) {
	input := Input{Kind: KindSandbox, Spec: map[string]any{"extensionIds": []any{"second", "first", "second"}}}
	Normalize(&input)
	if got := input.Spec["extensionIds"]; !reflect.DeepEqual(got, []string{"second", "first"}) {
		t.Fatalf("deduplicated IDs = %#v", got)
	}
	for _, invalid := range []any{[]any{"first", 1}, "first"} {
		input.Spec["extensionIds"] = invalid
		Normalize(&input)
		if _, err := DecodeResourceSpec(input); err == nil {
			t.Fatalf("accepted malformed IDs: %#v", invalid)
		}
	}
	input.Spec["extensionIds"] = []string{"../escape"}
	if err := validateExtensionIDs(input.Spec); err == nil {
		t.Fatal("accepted invalid extension ID")
	}
}

func TestSandboxExtensionObservationsCannotBecomeDesiredConfiguration(t *testing.T) {
	input := Input{Kind: KindSandbox, Spec: map[string]any{
		"extensionIds": []string{"chosen"}, "extensionSnapshots": []any{"forged"}, "extensionStates": []any{"forged"},
	}}
	decoded, err := DecodeResourceSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.(*SandboxSpec).ExtensionIDs; !reflect.DeepEqual(got, []string{"chosen"}) {
		t.Fatalf("desired IDs = %#v", got)
	}
	if len(DesiredResourceSpec(KindSandbox, input.Spec)) != 1 {
		t.Fatal("extension observations leaked into desired configuration")
	}
}

func TestExtensionProgressValidationBoundsOutput(t *testing.T) {
	input := WorkerJobProgressInput{ExtensionID: "test-extension", ExtensionStatus: "succeeded", ExtensionOutput: strings.Repeat("x", 4096)}
	if err := ValidateExtensionProgress(input); err != nil {
		t.Fatal(err)
	}
	input.ExtensionOutput += "x"
	if err := ValidateExtensionProgress(input); err == nil {
		t.Fatal("accepted oversized output")
	}
	input.ExtensionOutput = ""
	input.ExtensionStatus = "installed"
	if err := ValidateExtensionProgress(input); err == nil {
		t.Fatal("accepted unverified installed status")
	}
	input.ExtensionStatus = ""
	if err := ValidateExtensionProgress(input); err == nil {
		t.Fatal("accepted incomplete extension progress")
	}
}
