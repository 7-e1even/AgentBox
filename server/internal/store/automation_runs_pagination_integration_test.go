package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
)

func TestListAutomationRunsPageUsesStableCompositeCursor(t *testing.T) {
	s := newIntegrationTestStore(t)
	ctx := t.Context()
	projectID := "default"
	automationID := uuid.NewString()
	endpointID := uuid.NewString()
	search := "cursor-match-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	receivedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	secret := hashToken("automation-pagination-" + automationID)
	if _, err := s.pool.Exec(ctx, `INSERT INTO automations
	  (id, project_id, name, description, enabled, trigger_type, action_type, auth_mode,
	   endpoint_id, secret_hash, secret_ciphertext, secret_nonce, secret_last_four,
	   template_id, secret_rotated_at, created_at, updated_at)
	  VALUES ($1, $2, 'Cursor integration automation', '', TRUE, 'webhook',
	    'create-sandbox', 'bearer', $3, $4, $4, $4, 'test', NULL, $5, $5, $5)`,
		automationID, projectID, endpointID, secret, receivedAt); err != nil {
		t.Fatalf("insert pagination automation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM automation_runs WHERE automation_id = $1", automationID)
		_, _ = s.pool.Exec(context.Background(), "DELETE FROM automations WHERE id = $1", automationID)
	})

	insertRun := func(id, runProjectID string, status platform.AutomationRunStatus, templateName string) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `INSERT INTO automation_runs
		  (id, automation_id, project_id, automation_name, template_id, template_name,
		   trigger_source, auth_mode, payload_sha256, payload_bytes, status, received_at)
		  VALUES ($1, $2, $3, 'Cursor integration automation', 'cursor-template', $4,
		    'webhook', 'bearer', $5, 64, $6, $7)`, id, automationID, runProjectID,
			templateName, hashToken("payload-"+id), status, receivedAt); err != nil {
			t.Fatalf("insert automation run %s: %v", id, err)
		}
	}

	expectedIDs := make([]string, 0, 8)
	for range 8 {
		id := uuid.NewString()
		expectedIDs = append(expectedIDs, id)
		insertRun(id, projectID, platform.AutomationRunFailed, "Template "+search)
	}
	insertRun(uuid.NewString(), projectID, platform.AutomationRunSucceeded, "Template "+search)
	insertRun(uuid.NewString(), projectID, platform.AutomationRunFailed, "Template without search token")
	insertRun(uuid.NewString(), "other-"+uuid.NewString(), platform.AutomationRunFailed, "Template "+search)
	slices.Sort(expectedIDs)
	slices.Reverse(expectedIDs)

	const limit = 3
	seen := make(map[string]struct{}, len(expectedIDs))
	actualIDs := make([]string, 0, len(expectedIDs))
	cursor := ""
	pageCount := 0
	for {
		pageCount++
		if pageCount > len(expectedIDs) {
			t.Fatal("automation run pagination did not terminate")
		}
		page, err := s.ListAutomationRunsPage(ctx, platform.AutomationRunFilter{
			ProjectID: projectID, AutomationID: automationID,
			Status: platform.AutomationRunFailed, Search: search,
			Cursor: cursor, Limit: limit,
		})
		if err != nil {
			t.Fatalf("list automation run page %d: %v", pageCount, err)
		}
		if len(page.Items) == 0 {
			t.Fatalf("automation run page %d is unexpectedly empty", pageCount)
		}
		if len(page.Items) > limit {
			t.Fatalf("automation run page %d contains %d items, want at most %d", pageCount, len(page.Items), limit)
		}
		for _, run := range page.Items {
			if run.AutomationID == nil || *run.AutomationID != automationID || run.ProjectID != projectID ||
				run.Status != platform.AutomationRunFailed || !strings.Contains(run.TemplateName, search) {
				t.Fatalf("automation run page returned an item outside the filters: %#v", run)
			}
			if _, duplicate := seen[run.ID]; duplicate {
				t.Fatalf("automation run %s appeared on more than one page", run.ID)
			}
			seen[run.ID] = struct{}{}
			actualIDs = append(actualIDs, run.ID)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("final automation run page cursor = %q, want empty", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatalf("automation run page %d hasMore without nextCursor", pageCount)
		}
		if page.NextCursor == cursor {
			t.Fatalf("automation run page %d did not advance its cursor", pageCount)
		}
		cursor = page.NextCursor
	}
	if pageCount != 3 {
		t.Fatalf("automation run page count = %d, want 3", pageCount)
	}
	if !slices.Equal(actualIDs, expectedIDs) {
		t.Fatalf("automation run IDs = %v, want stable order %v", actualIDs, expectedIDs)
	}
}

func TestListAutomationRunsPageRejectsInvalidAutomationID(t *testing.T) {
	s := &Store{}
	_, err := s.ListAutomationRunsPage(t.Context(), platform.AutomationRunFilter{
		ProjectID: "default", AutomationID: "not-a-uuid", Limit: 10,
	})
	if !platform.IsValidationError(err) {
		t.Fatalf("invalid automation ID error = %v, want ValidationError", err)
	}
}
