package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestRenderAutomationPatchUsesPayloadAndFunctions(t *testing.T) {
	patch, err := renderAutomationPatch(`{
  "name": "pr-{{ .payload.number }}-{{ slug .payload.branch }}",
  "spec": {"environmentVariables": {{ toJson .payload.environmentVariables }}}
}`, map[string]any{
		"payload": map[string]any{
			"number": 17, "branch": "Feature/Preview One",
			"environmentVariables": []any{map[string]any{"name": "PR", "value": "17"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch["name"] != "pr-17-feature-preview-one" {
		t.Fatalf("name = %#v", patch["name"])
	}
	spec := patch["spec"].(map[string]any)
	if got := spec["environmentVariables"].([]any); len(got) != 1 {
		t.Fatalf("environment variables = %#v", got)
	}
}

func TestVerifyAutomationDeliveryBearerAndHMAC(t *testing.T) {
	secret := "abx_wh_test-secret"
	key := []byte("01234567890123456789012345678901")
	ciphertext, nonce, err := encryptSecret(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{secretKey: key}

	bearer := storedAutomation{
		Automation: platform.Automation{
			Trigger: platform.AutomationTriggerInput{AuthMode: platform.AutomationAuthBearer},
		},
		SecretHash: hashToken(secret),
	}
	if err := store.verifyAutomationDelivery(bearer, platform.AutomationDelivery{Authorization: "Bearer " + secret}); err != nil {
		t.Fatalf("bearer verification failed: %v", err)
	}
	if err := store.verifyAutomationDelivery(bearer, platform.AutomationDelivery{Authorization: "Bearer wrong"}); !errors.Is(err, ErrWebhookUnauthorized) {
		t.Fatalf("wrong bearer error = %v", err)
	}

	body := []byte(`{"event":"pull_request"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	hmacAutomation := storedAutomation{
		Automation: platform.Automation{
			Trigger: platform.AutomationTriggerInput{AuthMode: platform.AutomationAuthHMAC},
		},
		SecretCiphertext: ciphertext,
		SecretNonce:      nonce,
	}
	delivery := platform.AutomationDelivery{
		Timestamp: timestamp,
		Signature: "v1=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
		Body:      body,
	}
	if err := store.verifyAutomationDelivery(hmacAutomation, delivery); err != nil {
		t.Fatalf("HMAC verification failed: %v", err)
	}
	delivery.Body = []byte(`{"event":"tampered"}`)
	if err := store.verifyAutomationDelivery(hmacAutomation, delivery); !errors.Is(err, ErrWebhookUnauthorized) {
		t.Fatalf("tampered HMAC error = %v", err)
	}
}

func TestValidateAutomationIdempotencyKey(t *testing.T) {
	if err := validateAutomationIdempotencyKey("delivery-123"); err != nil {
		t.Fatal(err)
	}
	if err := validateAutomationIdempotencyKey(strings.Repeat("x", 256)); err == nil {
		t.Fatal("expected oversized idempotency key to fail")
	}
	if err := validateAutomationIdempotencyKey("line\nbreak"); err == nil {
		t.Fatal("expected control character to fail")
	}
}

func TestRenderAutomationPatchRejectsMissingPayloadField(t *testing.T) {
	_, err := renderAutomationPatch(`{"name":"{{ .payload.missing }}"}`, map[string]any{
		"payload": map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestMergeAutomationPatchUsesJSONMergePatchSemantics(t *testing.T) {
	result := mergeAutomationPatch(
		map[string]any{"keep": "yes", "remove": true, "spec": map[string]any{"cpu": "2", "list": []any{"a"}}},
		map[string]any{"remove": nil, "spec": map[string]any{"cpu": "4", "list": []any{"b"}}},
	)
	if _, exists := result["remove"]; exists {
		t.Fatal("null patch should remove the key")
	}
	want := map[string]any{"cpu": "4", "list": []any{"b"}}
	if got := result["spec"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec = %#v, want %#v", got, want)
	}
}

func TestBuildAutomatedSandboxInputProtectsIdentityAndLifecycleFields(t *testing.T) {
	projectID := "default"
	input, _, err := buildAutomatedSandboxInput(
		platform.AutomationInput{
			ProjectID: projectID, Name: "Preview", Enabled: true,
			Trigger: platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
			Action: platform.AutomationActionInput{
				Type: "create-sandbox", TemplateID: "runtime-one", ModelBindings: map[string]string{"credential-one": "model-one"},
				InputTemplate: `{"id":"attacker","kind":"runtime","projectId":"other","enabled":false,"spec":{"runtimeId":"other","status":"running","serverId":"override"}}`,
			},
		},
		"automation-one", "08a60394-b387-4d20-9033-6d72cc265c54",
		platform.Resource{Input: platform.Input{
			ID: "runtime-one", Kind: platform.KindRuntime, ProjectID: &projectID,
			Name: "Runtime One", Enabled: true, Spec: map[string]any{"serverId": "template"},
		}},
		map[string]any{}, map[string]string{}, map[string]any{}, time.Unix(0, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.ID == "attacker" || input.Kind != platform.KindSandbox || input.ProjectID == nil || *input.ProjectID != projectID || !input.Enabled {
		t.Fatalf("protected input fields changed: %#v", input)
	}
	if input.Spec["runtimeId"] != "runtime-one" || input.Spec["status"] != "requested" {
		t.Fatalf("protected spec fields changed: %#v", input.Spec)
	}
	if input.Spec["serverId"] != "override" {
		t.Fatalf("full spec override was not preserved: %#v", input.Spec)
	}
	bindings := specStringMap(input.Spec, "modelBindings")
	if bindings["credential-one"] != "model-one" {
		t.Fatalf("configured model binding was not applied: %#v", input.Spec)
	}
}
