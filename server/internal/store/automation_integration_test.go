package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestAutomationWebhookPersistsPollableIdempotentRun(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	secret := "integration-webhook-secret-" + uuid.NewString()
	templateID := "runtime-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	credentialID := "credential-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	modelID := "integration-model"
	if _, err := s.pool.Exec(ctx, `INSERT INTO provider_credentials
	  (id, name, provider_id, protocol, endpoint, models, secret_ciphertext,
	   secret_nonce, secret_last_four, enabled, created_at, updated_at)
	  VALUES ($1, 'Automation integration credential', 'openai', 'openai-chat', '',
	    $2::jsonb, '\\x00'::bytea, '\\x00'::bytea, 'test', TRUE, NOW(), NOW())`,
		credentialID, `[{"id":"`+modelID+`","name":"Integration model","group":"test","source":"manual"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES ($1, 'runtime', 'default', 'Automation integration template', '', TRUE,
	    jsonb_build_object('driver', 'docker', 'credentialIds', jsonb_build_array($2::text)),
	    NOW(), NOW())`, templateID, credentialID); err != nil {
		t.Fatal(err)
	}
	var automationID, runID string
	t.Cleanup(func() {
		if runID != "" {
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM automation_runs WHERE id = $1`, runID)
		}
		if automationID != "" {
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM automations WHERE id = $1`, automationID)
		}
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM control_resources WHERE id = $1`, templateID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM provider_credentials WHERE id = $1`, credentialID)
	})
	automation, _, err := s.CreateAutomation(ctx, platform.AutomationInput{
		ProjectID: "default", Name: "Integration webhook", Enabled: true,
		Secret:        secret,
		Trigger:       platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
		TemplateID:    templateID,
		ModelBindings: map[string]string{credentialID: modelID},
	}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	automationID = automation.ID
	if automation.ModelBindings[credentialID] != modelID {
		t.Fatalf("created model bindings = %#v", automation.ModelBindings)
	}
	stored, err := s.GetAutomation(ctx, automation.ID)
	if err != nil || stored.ModelBindings[credentialID] != modelID {
		t.Fatalf("stored model bindings = %#v, %v", stored.ModelBindings, err)
	}
	revealed, revealedSecret, err := s.GetAutomationSecret(ctx, automation.ID)
	if err != nil || revealed.ID != automation.ID || revealedSecret != secret {
		t.Fatalf("revealed automation = %#v secret matches = %v, %v", revealed, revealedSecret == secret, err)
	}

	delivery := platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret,
		IdempotencyKey: "integration-event-1", Body: []byte(`{"sandboxId":"unused"}`),
	}
	first, err := s.TriggerAutomation(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	runID = first.Run.ID
	if first.Run.Status != platform.AutomationRunFailed || first.StatusToken == "" {
		t.Fatalf("first result = %#v", first)
	}

	polled, err := s.GetPublicAutomationRun(ctx, automation.EndpointID, runID, first.StatusToken)
	if err != nil || polled.Status != platform.AutomationRunFailed {
		t.Fatalf("poll result = %#v, %v", polled, err)
	}
	duplicate, err := s.TriggerAutomation(ctx, delivery)
	if err != nil || !duplicate.Duplicate || duplicate.Run.ID != runID {
		t.Fatalf("duplicate result = %#v, %v", duplicate, err)
	}
	delivery.Body = []byte(`{"sandboxId":"different"}`)
	if _, err := s.TriggerAutomation(ctx, delivery); !errors.Is(err, ErrAutomationIdempotencyConflict) {
		t.Fatalf("conflicting payload error = %v", err)
	}
}
