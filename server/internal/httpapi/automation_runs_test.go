package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type pagedAutomationStore struct {
	fakeStore
	filter platform.AutomationRunFilter
}

func (s *pagedAutomationStore) ListAutomationRunsPage(_ context.Context, filter platform.AutomationRunFilter) (platform.AutomationRunPage, error) {
	s.filter = filter
	return platform.AutomationRunPage{
		Items:      []platform.AutomationRun{{ID: "12345678-1234-1234-1234-123456789012"}},
		NextCursor: "next-page", HasMore: true,
	}, nil
}

func TestListAutomationRunsUsesServerSideCursorAndFilters(t *testing.T) {
	store := &pagedAutomationStore{}
	server := &Server{store: store}
	request := httptest.NewRequest(http.MethodGet,
		"/api/automation-runs?projectId=default&automationId=auto-1&status=failed&search=timeout&cursor=current&limit=20", nil)
	response := httptest.NewRecorder()

	server.listAutomationRuns(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if store.filter.ProjectID != "default" || store.filter.AutomationID != "auto-1" ||
		store.filter.Status != platform.AutomationRunFailed || store.filter.Search != "timeout" ||
		store.filter.Cursor != "current" || store.filter.Limit != 20 {
		t.Fatalf("filter = %#v", store.filter)
	}
	var body struct {
		Items      []platform.AutomationRun `json:"items"`
		Runs       []platform.AutomationRun `json:"runs"`
		NextCursor string                   `json:"nextCursor"`
		HasMore    bool                     `json:"hasMore"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || len(body.Runs) != 1 || body.NextCursor != "next-page" || !body.HasMore {
		t.Fatalf("response = %#v", body)
	}
}

func TestAutomationRateLimitErrorPreservesRecoveryMetadata(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()

	server.handleAutomationTriggerError(response, store.ErrAutomationRateLimit)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "rate_limited" || body.Error.Message == "" || !body.Error.Retryable {
		t.Fatalf("error = %#v", body.Error)
	}
}
