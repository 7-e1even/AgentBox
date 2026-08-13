package platform

import "testing"

func TestValidateControlPlaneResource(t *testing.T) {
	projectID := "default"
	valid := Input{ID: "daily-review", Kind: KindSchedule, ProjectID: &projectID, Name: "Daily review", Enabled: true, Spec: map[string]any{"agentId": "agent-1", "cron": "0 9 * * *"}}
	if err := Validate(valid); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	valid.Spec["cron"] = "every day"
	if err := Validate(valid); !IsValidationError(err) {
		t.Fatalf("Validate() error = %v, want validation error", err)
	}
}
