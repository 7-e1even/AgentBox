package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func testEnvironmentSecretKey() []byte { return bytes.Repeat([]byte{7}, 32) }

func testEnvironmentSpec(name, value string) map[string]any {
	return map[string]any{
		"environmentVariables": []any{
			map[string]any{"name": name, "value": value},
		},
	}
}

func TestEnvironmentValueEncryptDecryptRoundTrip(t *testing.T) {
	key := testEnvironmentSecretKey()
	encrypted, err := encryptEnvironmentValue(key, "s3cr3t-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, encryptedEnvironmentPrefix) {
		t.Fatalf("encrypted value %q misses the %q prefix", encrypted, encryptedEnvironmentPrefix)
	}
	plaintext, err := decryptEnvironmentValue(key, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "s3cr3t-token" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestDecryptEnvironmentValuePassesThroughLegacyPlaintext(t *testing.T) {
	plaintext, err := decryptEnvironmentValue(testEnvironmentSecretKey(), "legacy-plain")
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "legacy-plain" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestDecryptEnvironmentValueRejectsTruncatedCiphertext(t *testing.T) {
	value := encryptedEnvironmentPrefix + base64.RawStdEncoding.EncodeToString([]byte("short"))
	if _, err := decryptEnvironmentValue(testEnvironmentSecretKey(), value); err == nil {
		t.Fatal("expected an error for a truncated ciphertext")
	}
}

func TestDecryptEnvironmentValueRejectsWrongKey(t *testing.T) {
	encrypted, err := encryptEnvironmentValue(testEnvironmentSecretKey(), "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := bytes.Repeat([]byte{8}, 32)
	if _, err := decryptEnvironmentValue(wrongKey, encrypted); err == nil {
		t.Fatal("expected an error when decrypting with the wrong key")
	}
}

func TestEncryptSpecEnvironmentVariablesEncryptsAndIsIdempotent(t *testing.T) {
	s := &Store{secretKey: testEnvironmentSecretKey()}
	spec := testEnvironmentSpec("API_KEY", "s3cr3t")
	if err := s.encryptSpecEnvironmentVariables(spec); err != nil {
		t.Fatal(err)
	}
	entry := spec["environmentVariables"].([]any)[0].(map[string]any)
	first, ok := entry["value"].(string)
	if !ok || !strings.HasPrefix(first, encryptedEnvironmentPrefix) {
		t.Fatalf("value = %v, want an %q-prefixed ciphertext", entry["value"], encryptedEnvironmentPrefix)
	}
	if err := s.encryptSpecEnvironmentVariables(spec); err != nil {
		t.Fatal(err)
	}
	if entry["value"] != first {
		t.Fatal("second encryption pass rewrote an already encrypted value")
	}
	plaintext, err := decryptEnvironmentValue(s.secretKey, first)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "s3cr3t" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestEncryptSpecEnvironmentVariablesRejectsMaskedValue(t *testing.T) {
	s := &Store{secretKey: testEnvironmentSecretKey()}
	spec := testEnvironmentSpec("API_KEY", environmentValueMask)
	var validationErr *platform.ValidationError
	if err := s.encryptSpecEnvironmentVariables(spec); !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
}

func TestMaskSpecEnvironmentVariables(t *testing.T) {
	spec := map[string]any{
		"environmentVariables": []any{
			map[string]any{"name": "API_KEY", "value": "s3cr3t"},
			map[string]any{"name": "REGION", "value": "cn-hangzhou"},
			map[string]any{"name": "BROKEN", "value": 42},
		},
	}
	maskSpecEnvironmentVariables(spec)
	variables := spec["environmentVariables"].([]any)
	for index, variable := range variables {
		entry := variable.(map[string]any)
		if index == 2 {
			if entry["value"] != 42 {
				t.Fatalf("non-string value was masked: %v", entry["value"])
			}
			continue
		}
		if entry["value"] != environmentValueMask {
			t.Fatalf("value = %v, want the fixed mask", entry["value"])
		}
	}
	if variables[0].(map[string]any)["name"] != "API_KEY" {
		t.Fatal("masking must not touch variable names")
	}
}

func TestResolveMaskedEnvironmentVariables(t *testing.T) {
	variables := []any{
		map[string]any{"name": "API_KEY", "value": environmentValueMask},
		map[string]any{"name": "REGION", "value": "cn-hangzhou"},
	}
	stored := map[string]string{"API_KEY": "enc:stored"}
	if err := resolveMaskedEnvironmentVariables(variables, stored); err != nil {
		t.Fatal(err)
	}
	if got := variables[0].(map[string]any)["value"]; got != "enc:stored" {
		t.Fatalf("value = %v, want the stored value", got)
	}
	if got := variables[1].(map[string]any)["value"]; got != "cn-hangzhou" {
		t.Fatalf("unmasked value was rewritten: %v", got)
	}
}

func TestResolveMaskedEnvironmentVariablesRejectsUnknownName(t *testing.T) {
	variables := []any{map[string]any{"name": "MISSING", "value": environmentValueMask}}
	var validationErr *platform.ValidationError
	if err := resolveMaskedEnvironmentVariables(variables, map[string]string{}); !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
}

func TestDecryptPayloadEnvironmentVariables(t *testing.T) {
	key := testEnvironmentSecretKey()
	s := &Store{secretKey: key}
	encrypted, err := encryptEnvironmentValue(key, "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"environmentVariables": []any{
			map[string]any{"name": "API_KEY", "value": encrypted},
			map[string]any{"name": "LEGACY", "value": "plain"},
		},
	}
	if err := s.decryptPayloadEnvironmentVariables(payload); err != nil {
		t.Fatal(err)
	}
	variables := payload["environmentVariables"].([]any)
	if got := variables[0].(map[string]any)["value"]; got != "s3cr3t" {
		t.Fatalf("value = %v, want decrypted plaintext", got)
	}
	if got := variables[1].(map[string]any)["value"]; got != "plain" {
		t.Fatalf("legacy plaintext was rewritten: %v", got)
	}
}
