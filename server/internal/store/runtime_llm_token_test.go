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
	token, err := repository.issueRuntimeLLMTokenForEpoch(
		"sandbox-one", "credential-one", "model-one", "restart-job-one", expiresAt,
	)
	if err != nil {
		t.Fatalf("issueRuntimeLLMToken() error = %v", err)
	}
	claims, err := repository.parseRuntimeLLMToken(token, time.Now().UTC())
	if err != nil {
		t.Fatalf("parseRuntimeLLMToken() error = %v", err)
	}
	if claims.SandboxID != "sandbox-one" || claims.CredentialID != "credential-one" ||
		claims.ModelID != "model-one" || claims.Epoch != "restart-job-one" {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestEpochRuntimeLLMTokenCanOutliveExpiry(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	now := time.Now().UTC()
	token, err := repository.issueRuntimeLLMTokenForEpoch(
		"sandbox-one", "credential-one", "model-one", "active-epoch", now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf("issue expired epoch token error = %v", err)
	}
	claims, err := repository.parseRuntimeLLMToken(token, now)
	if err != nil {
		t.Fatalf("parse expired epoch token error = %v", err)
	}
	if claims.Epoch != "active-epoch" {
		t.Fatalf("expired epoch token claims = %#v", claims)
	}
}

func TestRuntimeLLMTokenWithoutEpochRemainsParseableForLegacySandboxes(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	token, err := repository.issueRuntimeLLMToken(
		"sandbox-one", "credential-one", "model-one", time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issueRuntimeLLMToken() error = %v", err)
	}
	claims, err := repository.parseRuntimeLLMToken(token, time.Now().UTC())
	if err != nil {
		t.Fatalf("parseRuntimeLLMToken() error = %v", err)
	}
	if claims.Epoch != "" {
		t.Fatalf("legacy claims epoch = %q, want empty", claims.Epoch)
	}
}

func TestRuntimeLLMTokenRejectsTamperingAndLegacyExpiry(t *testing.T) {
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
