package store

import (
	"context"
	"errors"
	"testing"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestAutomationWebhookPersistsPollableIdempotentRun(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := context.Background()
	secret := "integration-webhook-secret-" + uuid.NewString()
	automation, _, err := s.CreateAutomation(ctx, platform.AutomationInput{
		ProjectID: "default", Name: "Integration webhook", Enabled: true,
		ConditionTemplate: "false", Secret: secret,
		Trigger: platform.AutomationTriggerInput{Type: "webhook", AuthMode: platform.AutomationAuthBearer},
		Action: platform.AutomationActionInput{
			Type: "destroy-sandbox", TargetTemplate: `{{ .payload.sandboxId }}`,
			CleanupPolicy: platform.AutomationCleanupNever,
		},
	}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var runID string
	t.Cleanup(func() {
		if runID != "" {
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM automation_runs WHERE id = $1`, runID)
		}
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM automations WHERE id = $1`, automation.ID)
	})

	delivery := platform.AutomationDelivery{
		EndpointID: automation.EndpointID, Authorization: "Bearer " + secret,
		IdempotencyKey: "integration-event-1", Body: []byte(`{"sandboxId":"unused"}`),
	}
	first, err := s.TriggerAutomation(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	runID = first.Run.ID
	if first.Run.Status != platform.AutomationRunSkipped || first.StatusToken == "" {
		t.Fatalf("first result = %#v", first)
	}

	polled, err := s.GetPublicAutomationRun(ctx, automation.EndpointID, runID, first.StatusToken)
	if err != nil || polled.Status != platform.AutomationRunSkipped {
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
