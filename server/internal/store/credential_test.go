package store

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestEncryptSecretDoesNotStorePlaintext(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	secret := "sk-agentbox-example-1234"
	ciphertext, nonce, err := encryptSecret(key, secret)
	if err != nil {
		t.Fatalf("encryptSecret() returned error: %v", err)
	}
	if len(nonce) == 0 {
		t.Fatal("encryptSecret() returned an empty nonce")
	}
	if bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatal("ciphertext contains the plaintext secret")
	}
	plaintext, err := decryptSecret(key, ciphertext, nonce)
	if err != nil {
		t.Fatalf("decryptSecret() returned error: %v", err)
	}
	if plaintext != secret {
		t.Fatalf("decryptSecret() = %q, want original secret", plaintext)
	}
	if got := lastFour(secret); got != "1234" {
		t.Fatalf("lastFour() = %q, want %q", got, "1234")
	}
}

func TestParseCredentialModels(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []platform.CredentialModel
	}{
		{
			name: "openai compatible",
			body: `{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"}]}`,
			want: []platform.CredentialModel{
				{ID: "gpt-5", Name: "gpt-5", Group: "gpt", Source: "remote"},
				{ID: "gpt-5-mini", Name: "gpt-5-mini", Group: "gpt", Source: "remote"},
			},
		},
		{
			name: "gemini",
			body: `{"models":[{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"}]}`,
			want: []platform.CredentialModel{{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Group: "gemini", Source: "remote"}},
		},
		{
			name: "deduplicates and sorts",
			body: `{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"a-model"}]}`,
			want: []platform.CredentialModel{
				{ID: "a-model", Name: "a-model", Group: "a", Source: "remote"},
				{ID: "z-model", Name: "z-model", Group: "z", Source: "remote"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCredentialModels(strings.NewReader(test.body))
			if err != nil {
				t.Fatalf("parseCredentialModels() returned error: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseCredentialModels() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestMergeCredentialModelsPreservesManualAndDefault(t *testing.T) {
	existing := []platform.CredentialModel{
		{ID: "manual-model", Name: "Manual", Group: "manual", Source: "manual"},
		{ID: "old-default", Name: "Old Default", Group: "old", Source: "remote"},
		{ID: "old-remote", Name: "Old Remote", Group: "old", Source: "remote"},
	}
	remote := []platform.CredentialModel{
		{ID: "new-model", Name: "New", Group: "new", Source: "remote"},
	}
	want := []platform.CredentialModel{
		{ID: "manual-model", Name: "Manual", Group: "manual", Source: "manual"},
		{ID: "new-model", Name: "New", Group: "new", Source: "remote"},
		{ID: "old-default", Name: "Old Default", Group: "old", Source: "remote"},
	}
	if got := mergeCredentialModels(existing, remote, "old-default"); !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeCredentialModels() = %#v, want %#v", got, want)
	}
}

func TestKimiCodingConnectionUsesMessagesEndpoint(t *testing.T) {
	request, err := providerCheckRequest(
		context.Background(),
		"anthropic",
		"anthropic",
		"https://api.kimi.com/coding/",
		"sk-kimi-example",
	)
	if err != nil {
		t.Fatalf("providerCheckRequest() returned error: %v", err)
	}
	if request.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", request.Method)
	}
	if got := request.URL.String(); got != "https://api.kimi.com/coding/v1/messages" {
		t.Fatalf("url = %q", got)
	}
	if got := request.Header.Get("x-api-key"); got != "sk-kimi-example" {
		t.Fatalf("x-api-key = %q", got)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if !strings.Contains(string(body), `"model":"kimi-for-coding"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestKimiCodingModelsAreAvailableWithoutModelsEndpoint(t *testing.T) {
	models := knownCredentialModels("anthropic", "https://api.kimi.com/coding")
	if len(models) != 4 {
		t.Fatalf("len(models) = %d, want 4", len(models))
	}
	if models[2].ID != "kimi-for-coding" {
		t.Fatalf("models[2].ID = %q", models[2].ID)
	}
	if models[2].Name != "Kimi K2.7 Code" || models[2].Group != "Kimi Code" {
		t.Fatalf("models[2] = %#v", models[2])
	}
	if got := knownCredentialModels("anthropic", "https://api.kimi.com.evil.example/coding"); got != nil {
		t.Fatalf("lookalike host returned models: %#v", got)
	}
}

func TestNormalizeKnownProviderEndpointKeepsProtocolAndKimiPathAligned(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		endpoint string
		want     string
	}{
		{
			name:     "anthropic removes openai suffix",
			protocol: "anthropic",
			endpoint: "https://api.kimi.com/coding/v1",
			want:     "https://api.kimi.com/coding/",
		},
		{
			name:     "openai adds v1 suffix",
			protocol: "openai-chat",
			endpoint: "https://api.kimi.com/coding/",
			want:     "https://api.kimi.com/coding/v1",
		},
		{
			name:     "unrecognized host is untouched",
			protocol: "anthropic",
			endpoint: "https://api.kimi.com.evil.example/coding/v1",
			want:     "https://api.kimi.com.evil.example/coding/v1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeKnownProviderEndpoint(test.protocol, test.endpoint); got != test.want {
				t.Fatalf("normalizeKnownProviderEndpoint() = %q, want %q", got, test.want)
			}
		})
	}
}
