package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRuntimeLLMTokenRoundTrip(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	expiresAt := time.Now().UTC().Add(time.Hour)
	token, err := repository.issueRuntimeLLMToken("sandbox-one", "credential-one", "model-one", expiresAt)
	if err != nil {
		t.Fatalf("issueRuntimeLLMToken() error = %v", err)
	}
	claims, err := repository.parseRuntimeLLMToken(token, time.Now().UTC())
	if err != nil {
		t.Fatalf("parseRuntimeLLMToken() error = %v", err)
	}
	if claims.SandboxID != "sandbox-one" || claims.CredentialID != "credential-one" || claims.ModelID != "model-one" {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestRuntimeLLMTokenRejectsTamperingAndExpiry(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	token, err := repository.issueRuntimeLLMToken(
		"sandbox-one", "credential-one", "model-one", time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issueRuntimeLLMToken() error = %v", err)
	}
	parts := strings.Split(token, ".")
	replacement := "A"
	if strings.HasPrefix(parts[2], replacement) {
		replacement = "B"
	}
	parts[2] = replacement + parts[2][1:]
	tampered := strings.Join(parts, ".")
	if _, err := repository.parseRuntimeLLMToken(tampered, time.Now().UTC()); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("tampered token error = %v, want ErrRuntimeUnauthorized", err)
	}
	expired, err := repository.issueRuntimeLLMToken(
		"sandbox-one", "credential-one", "model-one", time.Now().UTC().Add(-time.Second),
	)
	if err != nil {
		t.Fatalf("issue expired token error = %v", err)
	}
	if _, err := repository.parseRuntimeLLMToken(expired, time.Now().UTC()); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("expired token error = %v, want ErrRuntimeUnauthorized", err)
	}
}
