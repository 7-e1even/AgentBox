package store

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agentbox/internal/platform"
)

func TestAutomatedSandboxNameStaysWithinResourceLimit(t *testing.T) {
	name := automatedSandboxName(strings.Repeat("沙", 80), "12345678")
	if got := utf8.RuneCountInString(name); got != 80 {
		t.Fatalf("generated name length = %d, want 80", got)
	}
	if !strings.HasSuffix(name, "-12345678") {
		t.Fatalf("generated name = %q, missing run suffix", name)
	}
}

func TestAutomationRunCursorRoundTrip(t *testing.T) {
	run := platform.AutomationRun{
		ID:         "12345678-1234-1234-1234-123456789012",
		ReceivedAt: time.Date(2026, time.August, 30, 9, 8, 7, 654321000, time.FixedZone("CST", 8*60*60)),
	}
	receivedAt, id, err := decodeAutomationRunCursor(encodeAutomationRunCursor(run))
	if err != nil {
		t.Fatal(err)
	}
	if id != run.ID || !receivedAt.Equal(run.ReceivedAt) {
		t.Fatalf("decoded cursor = %s %s, want %s %s", receivedAt, id, run.ReceivedAt, run.ID)
	}
}

func TestAutomationRunCursorRejectsInvalidValues(t *testing.T) {
	for _, cursor := range []string{"not-base64", "bm8tc2VwYXJhdG9y", "MjAyNi0wOC0zMFQwMTowMjowM1oKbm90LWEtdXVpZA"} {
		if _, _, err := decodeAutomationRunCursor(cursor); err == nil {
			t.Fatalf("cursor %q was accepted", cursor)
		}
	}
}

func TestBuildAutomatedSandboxInputInjectsModelBindings(t *testing.T) {
	automation := platform.Automation{
		ID: "automation-one", ProjectID: "default", Name: "PR preview",
		ModelBindings: map[string]string{"credential-one": "model-one"},
	}
	template := platform.Resource{
		Input: platform.Input{
			ID: "runtime-one",
			Spec: map[string]any{
				"driver":        "docker",
				"credentialIds": []any{"credential-one"},
			},
		},
	}

	input, _, err := buildAutomatedSandboxInput(automation, "12345678-1234-1234-1234-123456789012", template)
	if err != nil {
		t.Fatal(err)
	}
	if got := specStringMap(input.Spec, "modelBindings"); !reflect.DeepEqual(got, automation.ModelBindings) {
		t.Fatalf("model bindings = %#v, want %#v", got, automation.ModelBindings)
	}
}
