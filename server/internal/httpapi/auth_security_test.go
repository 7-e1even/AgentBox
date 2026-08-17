package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
)

func loginRequest(t *testing.T, handler http.Handler, username string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"password123"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLoginRateLimitReturns429(t *testing.T) {
	handler := rawTestHandler()
	for attempt := 1; attempt <= loginRateLimitAttempts; attempt++ {
		response := loginRequest(t, handler, "admin")
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want %d (body = %s)", attempt, response.Code, http.StatusOK, response.Body.String())
		}
	}
	response := loginRequest(t, handler, "admin")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d: status = %d, want %d", loginRateLimitAttempts+1, response.Code, http.StatusTooManyRequests)
	}
}

func TestLoginRateLimitCannotBeBypassedWithRotatingUsernames(t *testing.T) {
	handler := rawTestHandler()
	for attempt := 1; attempt <= loginRateLimitAttempts; attempt++ {
		response := loginRequest(t, handler, fmt.Sprintf("user-%d", attempt))
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want %d", attempt, response.Code, http.StatusOK)
		}
	}
	response := loginRequest(t, handler, "another-user")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
}

func TestLoginRateLimiterSlidingWindow(t *testing.T) {
	limiter := newLoginRateLimiter()
	now := time.Now().UTC()
	for attempt := 1; attempt <= loginRateLimitAttempts; attempt++ {
		if !limiter.allow("192.0.2.1", now) {
			t.Fatalf("attempt %d should be allowed", attempt)
		}
	}
	if limiter.allow("192.0.2.1", now) {
		t.Fatal("attempt beyond the limit should be rejected")
	}
	if !limiter.allow("192.0.2.1", now.Add(loginRateLimitWindow+time.Second)) {
		t.Fatal("attempts outside the sliding window should be allowed again")
	}
}

func TestLoginRateLimiterCapsActiveKeys(t *testing.T) {
	limiter := newLoginRateLimiter()
	now := time.Now().UTC()
	for index := range loginRateLimitMaxKeys {
		if !limiter.allow(fmt.Sprintf("192.0.2.%d", index), now) {
			t.Fatalf("key %d should be allowed", index)
		}
	}
	if limiter.allow("198.51.100.1", now) {
		t.Fatal("new key beyond the active-key cap should be rejected")
	}
}

func TestAuthBearerParsing(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"BEARER abc123", "abc123"},
		{"Bearer   spaced  ", "spaced"},
		{"Bearer", ""},
		{"Basic abc123", ""},
		{"", ""},
	}
	for _, testCase := range cases {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		if testCase.header != "" {
			request.Header.Set("Authorization", testCase.header)
		}
		if got := authBearer(request); got != testCase.want {
			t.Errorf("authBearer(%q) = %q, want %q", testCase.header, got, testCase.want)
		}
	}
}

func TestSessionCookieIgnoresForwardedProtoByDefault(t *testing.T) {
	t.Setenv("AGENTBOX_TRUSTED_PROXY", "")
	handler := rawTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"password123"}`))
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure {
		t.Fatalf("不信任代理时 Cookie 不应因 X-Forwarded-Proto 带 Secure: %#v", cookies)
	}
}

func TestSessionCookieHonorsForwardedProtoWhenProxyTrusted(t *testing.T) {
	t.Setenv("AGENTBOX_TRUSTED_PROXY", "true")
	handler := rawTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"password123"}`))
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("信任代理时 Cookie 应带 Secure: %#v", cookies)
	}
}

func TestWorkerRequestBaseURLRespectsTrustedProxy(t *testing.T) {
	build := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "http://internal.example:8091/x", nil)
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Forwarded-Host", "public.example")
		return request
	}
	if got := workerRequestBaseURL(build(), false); got != "http://internal.example:8091" {
		t.Errorf("trustedProxy=false: got %q, want %q", got, "http://internal.example:8091")
	}
	if got := workerRequestBaseURL(build(), true); got != "https://public.example" {
		t.Errorf("trustedProxy=true: got %q, want %q", got, "https://public.example")
	}
}

func TestAcceptOptionsIgnoresForwardedHostUnlessProxyTrusted(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Forwarded-Host", "evil.example")

	hub := newSessionHub([]string{"http://localhost:3000"}, false)
	for _, pattern := range hub.acceptOptions(request).OriginPatterns {
		if pattern == "evil.example" {
			t.Fatal("trustedProxy=false 时不应把 X-Forwarded-Host 并入 Origin 白名单")
		}
	}

	trustedHub := newSessionHub([]string{"http://localhost:3000"}, true)
	found := false
	for _, pattern := range trustedHub.acceptOptions(request).OriginPatterns {
		if pattern == "evil.example" {
			found = true
		}
	}
	if !found {
		t.Fatal("trustedProxy=true 时应保留 X-Forwarded-Host 并入白名单的现状行为")
	}
}

type credentialModelCaptureStore struct {
	fakeStore
	gotModelID string
}

func (s *credentialModelCaptureStore) DeleteCredentialModel(_ context.Context, _ string, modelID string) ([]platform.CredentialModel, error) {
	s.gotModelID = modelID
	return []platform.CredentialModel{}, nil
}

func TestDeleteCredentialModelPathParameter(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"path parameter", "/api/credentials/openai-primary/models/custom-model", "custom-model"},
		{"path parameter url-encoded", "/api/credentials/openai-primary/models/gpt-5%2Fpro", "gpt-5/pro"},
		{"legacy query parameter", "/api/credentials/openai-primary/models?modelId=custom-model", "custom-model"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &credentialModelCaptureStore{}
			handler := New(store, catalog.BuiltinCatalog, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, Config{})
			request := httptest.NewRequest(http.MethodDelete, testCase.path, nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body = %s)", response.Code, http.StatusOK, response.Body.String())
			}
			if store.gotModelID != testCase.want {
				t.Fatalf("modelID = %q, want %q", store.gotModelID, testCase.want)
			}
		})
	}
}

func TestAccessLogSkipsHealthzAndRecordsStatus(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	handler := New(fakeStore{}, catalog.BuiltinCatalog, logger, nil, Config{})

	healthRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(httptest.NewRecorder(), healthRequest)
	if strings.Contains(buffer.String(), "healthz") {
		t.Fatalf("/healthz 不应产生访问日志: %s", buffer.String())
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	catalogRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	handler.ServeHTTP(httptest.NewRecorder(), catalogRequest)
	output := buffer.String()
	if !strings.Contains(output, "status=200") {
		t.Fatalf("访问日志缺少状态码: %s", output)
	}
	if !strings.Contains(output, "remote=192.0.2.1:1234") {
		t.Fatalf("访问日志缺少客户端地址: %s", output)
	}
}
