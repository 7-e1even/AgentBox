package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
)

const setupTestCode = "7YbO1FAfhFzx7oEFD6G07QTWvzCN_qSY"

type setupCaptureStore struct {
	fakeStore
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (store *setupCaptureStore) NeedsUserSetup(context.Context) (bool, error) { return true, nil }

func (store *setupCaptureStore) SetupAdmin(_ context.Context, input platform.UserInput, _ []byte, _ time.Time) (platform.User, error) {
	store.calls.Add(1)
	if store.entered != nil {
		store.entered <- struct{}{}
		<-store.release
	}
	return store.fakeStore.SetupAdmin(context.Background(), input, nil, time.Time{})
}

func newSetupTestHandler(store *setupCaptureStore) http.Handler {
	return New(store, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{SetupCode: setupTestCode})
}

func setupRequest(code string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{
        "name":"Administrator","username":"admin","email":"admin@example.com",
        "password":"password123","setupCode":"`+code+`"
    }`))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestSetupCodeValidation(t *testing.T) {
	for _, code := range []string{
		"short",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
		"not+raw/base64url-not-accepted!!",
	} {
		if err := ValidateSetupCode(code); err == nil {
			t.Fatalf("ValidateSetupCode(%q) succeeded", code)
		}
	}
	generated, err := GenerateSetupCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSetupCode(generated); err != nil {
		t.Fatalf("generated setup code rejected: %v", err)
	}
}

func TestEmbeddedServerRejectsInvalidSetupCode(t *testing.T) {
	store := &setupCaptureStore{}
	handler := New(store, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{SetupCode: "short"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, setupRequest("short"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if calls := store.calls.Load(); calls != 0 {
		t.Fatalf("store calls=%d, want 0", calls)
	}
}

func TestSetupCodeRequiredBeforeStoreCall(t *testing.T) {
	for _, code := range []string{"", "wrong-code"} {
		store := &setupCaptureStore{}
		response := httptest.NewRecorder()
		newSetupTestHandler(store).ServeHTTP(response, setupRequest(code))
		if response.Code != http.StatusForbidden {
			t.Fatalf("code=%q status=%d body=%s", code, response.Code, response.Body.String())
		}
		if calls := store.calls.Load(); calls != 0 {
			t.Fatalf("code=%q store calls=%d", code, calls)
		}
	}
}

func TestSuccessfulSetupCodeIsConsumed(t *testing.T) {
	store := &setupCaptureStore{}
	handler := newSetupTestHandler(store)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, setupRequest(setupTestCode))
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, setupRequest(setupTestCode))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if calls := store.calls.Load(); calls != 1 {
		t.Fatalf("store calls=%d, want 1", calls)
	}
}

func TestSetupAllowsOnlyOneInFlightRequest(t *testing.T) {
	store := &setupCaptureStore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	handler := newSetupTestHandler(store)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, setupRequest(setupTestCode))
		firstDone <- response
	}()
	<-store.entered

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, setupRequest(setupTestCode))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	close(store.release)
	first := <-firstDone
	if first.Code != http.StatusCreated || store.calls.Load() != 1 {
		t.Fatalf("first status=%d calls=%d", first.Code, store.calls.Load())
	}
}

func TestAuthStatusSignalsSetupCodeRequirement(t *testing.T) {
	response := httptest.NewRecorder()
	newSetupTestHandler(&setupCaptureStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"setupCodeRequired":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
