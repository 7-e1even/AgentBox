package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

func TestVerifyAutomationDeliveryProviderAdapters(t *testing.T) {
	secret := "pipeline-webhook-secret"
	key := []byte("01234567890123456789012345678901")
	ciphertext, nonce, err := encryptSecret(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{secretKey: key}
	body := []byte(`{"ref":"refs/heads/main"}`)
	stored := func(mode platform.AutomationAuthMode) storedAutomation {
		return storedAutomation{
			Automation: platform.Automation{Trigger: platform.AutomationTriggerInput{AuthMode: mode}},
			SecretHash: hashToken(secret), SecretCiphertext: ciphertext, SecretNonce: nonce,
		}
	}

	githubMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = githubMAC.Write(body)
	github := platform.AutomationDelivery{Body: body, Headers: map[string]string{
		"x-hub-signature-256": "sha256=" + hex.EncodeToString(githubMAC.Sum(nil)),
	}}
	if err := store.verifyAutomationDelivery(stored(platform.AutomationAuthGitHub), github); err != nil {
		t.Fatalf("GitHub verification failed: %v", err)
	}

	gitlab := platform.AutomationDelivery{Body: body, Headers: map[string]string{"x-gitlab-token": secret}}
	if err := store.verifyAutomationDelivery(stored(platform.AutomationAuthGitLab), gitlab); err != nil {
		t.Fatalf("GitLab verification failed: %v", err)
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	standardMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = standardMAC.Write([]byte("delivery-17." + timestamp + "."))
	_, _ = standardMAC.Write(body)
	standard := platform.AutomationDelivery{Body: body, Headers: map[string]string{
		"webhook-id": "delivery-17", "webhook-timestamp": timestamp,
		"webhook-signature": "v1," + base64.StdEncoding.EncodeToString(standardMAC.Sum(nil)),
	}}
	if err := store.verifyAutomationDelivery(stored(platform.AutomationAuthStandardWebhook), standard); err != nil {
		t.Fatalf("Standard Webhooks verification failed: %v", err)
	}
}

func TestCanonicalAutomationEventUsesProviderMetadata(t *testing.T) {
	receivedAt := time.Unix(100, 0).UTC()
	event := canonicalAutomationEvent(platform.AutomationDelivery{Headers: map[string]string{
		"x-github-delivery": "delivery-17", "x-github-event": "pull_request",
	}}, platform.AutomationAuthGitHub, receivedAt)
	if event.ID != "delivery-17" || event.Type != "pull_request" || event.Source != "github" || !event.Time.Equal(receivedAt) {
		t.Fatalf("event = %#v", event)
	}
}

func TestAutomationIdempotencyFallsBackToCanonicalEventID(t *testing.T) {
	hash, fingerprint := automationIdempotency(platform.AutomationDelivery{}, platform.AutomationAuthGitHub,
		platform.AutomationEvent{ID: "delivery-17"})
	if len(hash) != sha256.Size || len(fingerprint) != 12 {
		t.Fatalf("hash bytes = %d fingerprint = %q", len(hash), fingerprint)
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

func TestRenderAutomationConditionAndString(t *testing.T) {
	context := map[string]any{"event": map[string]any{"type": "pull_request"}, "payload": map[string]any{"sandboxId": "box-17"}}
	matched, err := renderAutomationCondition(`{{ eq .event.type "pull_request" }}`, context)
	if err != nil || !matched {
		t.Fatalf("condition = %v, %v", matched, err)
	}
	target, err := renderAutomationString("目标模板", `{{ .payload.sandboxId }}`, context)
	if err != nil || target != "box-17" {
		t.Fatalf("target = %q, %v", target, err)
	}
	if _, err := renderAutomationCondition(`yes`, context); err == nil {
		t.Fatal("expected non-boolean condition to fail")
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
		map[string]any{}, map[string]string{}, map[string]any{}, platform.AutomationEvent{
			Type: "event", Source: "generic", Time: time.Unix(0, 0).UTC(), ReceivedAt: time.Unix(0, 0).UTC(),
		},
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
