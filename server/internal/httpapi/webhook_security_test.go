package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type webhookCountingStore struct {
	fakeStore
	calls int
}

type webhookAuthenticatingStore struct {
	fakeStore
	endpointID string
	token      string
	calls      int
}

func (s *webhookAuthenticatingStore) TriggerAutomation(
	_ context.Context, delivery platform.AutomationDelivery,
) (platform.AutomationTriggerResult, error) {
	s.calls++
	if len(delivery.IdempotencyKey) > 255 {
		return platform.AutomationTriggerResult{}, &platform.ValidationError{Message: "invalid idempotency key"}
	}
	if delivery.EndpointID != s.endpointID {
		return platform.AutomationTriggerResult{}, store.ErrResourceNotFound
	}
	if delivery.Authorization != "Bearer "+s.token {
		return platform.AutomationTriggerResult{}, store.ErrWebhookUnauthorized
	}
	return platform.AutomationTriggerResult{}, nil
}

func (s *webhookCountingStore) TriggerAutomation(
	context.Context, platform.AutomationDelivery,
) (platform.AutomationTriggerResult, error) {
	s.calls++
	return platform.AutomationTriggerResult{}, nil
}

func TestWebhookRateLimitRejectsBeforeStore(t *testing.T) {
	repository := &webhookCountingStore{}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(1, 10, webhookRateLimitMaxKeys),
	}
	const endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"

	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+endpointID, strings.NewReader(`{}`))
		request.SetPathValue("endpointId", endpointID)
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		return response
	}

	if response := send(); response.Code != http.StatusAccepted {
		t.Fatalf("first request status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := send(); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("limited request status = %d, Retry-After = %q", response.Code, response.Header().Get("Retry-After"))
	}
	if repository.calls != 1 {
		t.Fatalf("TriggerAutomation calls = %d, want 1", repository.calls)
	}
}

func TestBoundedRateLimiterUsesDeterministicWindowAndKeyCap(t *testing.T) {
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)
	limiter := newBoundedRateLimiter(1, time.Minute, 1)
	if !limiter.allow("first", now) || limiter.allow("first", now) {
		t.Fatal("limiter did not enforce the configured attempt count")
	}
	if limiter.allow("second", now) {
		t.Fatal("limiter accepted a new key beyond its cap")
	}
	if !limiter.allow("second", now.Add(time.Minute)) {
		t.Fatal("limiter did not release expired entries at the window boundary")
	}
}

func TestUnknownWebhookEndpointsDoNotExhaustAuthenticatedGlobalCapacity(t *testing.T) {
	const (
		endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"
		token      = "valid-secret"
	)
	repository := &webhookAuthenticatingStore{endpointID: endpointID, token: token}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(webhookRateLimitAttempts, webhookGlobalRateLimitAttempts, webhookRateLimitMaxKeys),
	}

	for index := range webhookGlobalRateLimitAttempts {
		unknownID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+unknownID, strings.NewReader(`{}`))
		request.RemoteAddr = fmt.Sprintf("192.0.%d.%d:1234", index/254, index%254+1)
		request.SetPathValue("endpointId", unknownID)
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown request %d status = %d, body = %s", index, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+endpointID, strings.NewReader(`{}`))
	request.RemoteAddr = "198.51.100.1:1234"
	request.Header.Set("Authorization", "Bearer "+token)
	request.SetPathValue("endpointId", endpointID)
	response := httptest.NewRecorder()
	server.receiveAutomationWebhook(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("authenticated request status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestWebhookPreAuthenticationRateLimitSpansEndpointsPerIP(t *testing.T) {
	const endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"
	repository := &webhookAuthenticatingStore{endpointID: endpointID, token: "valid-secret"}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(2, 10, webhookRateLimitMaxKeys),
	}

	for index := range 3 {
		unknownID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+unknownID, strings.NewReader(`{}`))
		request.RemoteAddr = "192.0.2.1:1234"
		request.SetPathValue("endpointId", unknownID)
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		if index < 2 && response.Code != http.StatusNotFound {
			t.Fatalf("request %d status = %d, want %d", index, response.Code, http.StatusNotFound)
		}
		if index == 2 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d status = %d, want %d", index, response.Code, http.StatusTooManyRequests)
		}
	}
	if repository.calls != 2 {
		t.Fatalf("TriggerAutomation calls = %d, want 2", repository.calls)
	}
}

func TestMalformedWebhookEndpointIsPreAuthenticationRateLimited(t *testing.T) {
	repository := &webhookCountingStore{}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(2, 10, webhookRateLimitMaxKeys),
	}

	for index, wantStatus := range []int{
		http.StatusNotFound,
		http.StatusNotFound,
		http.StatusTooManyRequests,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/not-a-uuid", strings.NewReader(`{}`))
		request.RemoteAddr = "192.0.2.1:1234"
		request.SetPathValue("endpointId", "not-a-uuid")
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d", index, response.Code, wantStatus)
		}
	}
	if repository.calls != 0 {
		t.Fatalf("TriggerAutomation calls = %d, want 0", repository.calls)
	}
}

func TestWebhookBusinessRateLimitSpansClientsPerEndpoint(t *testing.T) {
	const (
		endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"
		token      = "valid-secret"
	)
	repository := &webhookAuthenticatingStore{endpointID: endpointID, token: token}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(1, 10, webhookRateLimitMaxKeys),
	}

	for index, remoteAddr := range []string{"192.0.2.1:1234", "198.51.100.1:1234"} {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+endpointID, strings.NewReader(`{}`))
		request.RemoteAddr = remoteAddr
		request.Header.Set("Authorization", "Bearer "+token)
		request.SetPathValue("endpointId", endpointID)
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		wantStatus := http.StatusAccepted
		if index == 1 {
			wantStatus = http.StatusTooManyRequests
		}
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d", index, response.Code, wantStatus)
		}
	}
	if repository.calls != 1 {
		t.Fatalf("TriggerAutomation calls = %d, want 1", repository.calls)
	}
}

func TestWebhookAggregatePreAuthenticationLimitBoundsDistributedSources(t *testing.T) {
	const businessGlobalAttempts = 2
	server := &Server{
		store:          &webhookCountingStore{},
		webhookLimiter: newWebhookRateLimiter(1, businessGlobalAttempts, webhookRateLimitMaxKeys),
	}
	limit := businessGlobalAttempts * webhookPreAuthGlobalMultiplier
	for index := range limit + 1 {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/not-a-uuid", strings.NewReader(`{}`))
		request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", index+1)
		request.SetPathValue("endpointId", "not-a-uuid")
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		wantStatus := http.StatusNotFound
		if index == limit {
			wantStatus = http.StatusTooManyRequests
		}
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d", index, response.Code, wantStatus)
		}
	}
}

func TestWebhookBusinessReservationIsConcurrentAndReleasable(t *testing.T) {
	const endpointLimit = 5
	limiter := newWebhookRateLimiter(endpointLimit, 10, webhookRateLimitMaxKeys)
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan bool, 64)
	var group sync.WaitGroup
	for range cap(results) {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- limiter.reserveBusinessCapacity("shared-endpoint", now)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result {
			accepted++
		}
	}
	if accepted != endpointLimit {
		t.Fatalf("concurrent accepted reservations = %d, want %d", accepted, endpointLimit)
	}
	for range accepted {
		limiter.releaseBusinessCapacity("shared-endpoint", now)
	}
	for index := range endpointLimit + 1 {
		accepted := limiter.reserveBusinessCapacity("shared-endpoint", now.Add(time.Second))
		if accepted != (index < endpointLimit) {
			t.Fatalf("reservation %d accepted = %v", index, accepted)
		}
	}
}

func TestFailedWebhookAuthenticationReleasesBusinessCapacity(t *testing.T) {
	const endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"
	repository := &webhookAuthenticatingStore{endpointID: endpointID, token: "valid-secret"}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(10, 1, webhookRateLimitMaxKeys),
	}

	send := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+endpointID, strings.NewReader(`{}`))
		request.RemoteAddr = "192.0.2.1:1234"
		request.Header.Set("Authorization", "Bearer "+token)
		request.SetPathValue("endpointId", endpointID)
		response := httptest.NewRecorder()
		server.receiveAutomationWebhook(response, request)
		return response
	}

	if response := send("invalid"); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := send("valid-secret"); response.Code != http.StatusAccepted {
		t.Fatalf("authenticated request status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := send("valid-secret"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("second authenticated request status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
}

func TestPreAuthenticationValidationFailureReleasesBusinessCapacity(t *testing.T) {
	const endpointID = "75778270-bdbf-4e2f-bbeb-b3133447a367"
	repository := &webhookAuthenticatingStore{endpointID: endpointID, token: "valid-secret"}
	server := &Server{
		store:          repository,
		webhookLimiter: newWebhookRateLimiter(10, 1, webhookRateLimitMaxKeys),
	}

	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/webhooks/00000000-0000-4000-8000-000000000000", strings.NewReader(`{}`))
	invalidRequest.RemoteAddr = "192.0.2.1:1234"
	invalidRequest.Header.Set("Idempotency-Key", strings.Repeat("x", 256))
	invalidRequest.SetPathValue("endpointId", "00000000-0000-4000-8000-000000000000")
	invalidResponse := httptest.NewRecorder()
	server.receiveAutomationWebhook(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status = %d, body = %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	validRequest := httptest.NewRequest(http.MethodPost, "/api/webhooks/"+endpointID, strings.NewReader(`{}`))
	validRequest.RemoteAddr = "198.51.100.1:1234"
	validRequest.Header.Set("Authorization", "Bearer valid-secret")
	validRequest.SetPathValue("endpointId", endpointID)
	validResponse := httptest.NewRecorder()
	server.receiveAutomationWebhook(validResponse, validRequest)
	if validResponse.Code != http.StatusAccepted {
		t.Fatalf("authenticated request status = %d, body = %s", validResponse.Code, validResponse.Body.String())
	}
}
