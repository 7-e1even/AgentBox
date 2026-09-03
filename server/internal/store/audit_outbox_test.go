package store

import "testing"

func TestAuditMetadataKeepsModelSourceTransition(t *testing.T) {
	detail := auditMetadata(map[string]any{
		"slotCredentialId": "primary",
		"fromCredentialId": "anthropic-primary",
		"fromModelId":      "claude-sonnet-4-5",
		"toCredentialId":   "openai-primary",
		"toModelId":        "gpt-5.4",
		"secret":           "must-not-survive",
	})

	for _, key := range []string{
		"slotCredentialId", "fromCredentialId", "fromModelId", "toCredentialId", "toModelId",
	} {
		if detail[key] == "" {
			t.Fatalf("audit metadata omitted %s", key)
		}
	}
	if _, ok := detail["secret"]; ok {
		t.Fatal("audit metadata retained a secret")
	}
}
