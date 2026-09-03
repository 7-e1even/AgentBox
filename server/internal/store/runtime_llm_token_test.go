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

func TestValidateRuntimeLLMTokenRequiresMatchingSignedPathClaims(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	token, err := repository.issueRuntimeLLMToken(
		"sandbox-one", "credential-one", "model-one", time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("issueRuntimeLLMToken() error = %v", err)
	}
	if err := repository.ValidateRuntimeLLMToken("sandbox-one", "credential-one", token); err != nil {
		t.Fatalf("ValidateRuntimeLLMToken() error = %v", err)
	}
	for _, path := range [][2]string{
		{"sandbox-two", "credential-one"},
		{"sandbox-one", "credential-two"},
	} {
		if err := repository.ValidateRuntimeLLMToken(path[0], path[1], token); !errors.Is(err, ErrRuntimeUnauthorized) {
			t.Fatalf("ValidateRuntimeLLMToken(%q, %q) error = %v, want ErrRuntimeUnauthorized", path[0], path[1], err)
		}
	}
}

func TestEpochRuntimeLLMTokenRejectsExpiry(t *testing.T) {
	repository := &Store{secretKey: []byte("0123456789abcdef0123456789abcdef")}
	now := time.Now().UTC()
	token, err := repository.issueRuntimeLLMTokenForEpoch(
		"sandbox-one", "credential-one", "model-one", "active-epoch", now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf("issue expired epoch token error = %v", err)
	}
	if _, err := repository.parseRuntimeLLMToken(token, now); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("parse expired epoch token error = %v, want ErrRuntimeUnauthorized", err)
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
	oversized := strings.Repeat("x", runtimeLLMTokenMaxBytes+1)
	if _, err := repository.parseRuntimeLLMToken(oversized, time.Now().UTC()); !errors.Is(err, ErrRuntimeUnauthorized) {
		t.Fatalf("oversized token error = %v, want ErrRuntimeUnauthorized", err)
	}
}
