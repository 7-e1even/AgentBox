package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"agentbox/internal/platform"
	"agentbox/internal/skillimport"
)

func TestSkillsSearchUsesOperatorRoleAndValidatesQuery(t *testing.T) {
	for _, role := range []platform.UserRole{platform.UserRoleViewer, platform.UserRoleOperator, platform.UserRoleAdmin} {
		response := rbacRequest(t, rbacHandler(role), http.MethodGet, "/api/skills/search?q=x", "")
		expected := http.StatusBadRequest
		if role == platform.UserRoleViewer {
			expected = http.StatusForbidden
		}
		if response.Code != expected {
			t.Fatalf("search as %s returned %d, want %d", role, response.Code, expected)
		}
	}
}

func TestSkillImportPreviewUsesExistingRoleGate(t *testing.T) {
	document := "---\nname: test-skill\ndescription: Test import\n---\n\nReview project files.\n"
	body, err := json.Marshal(map[string]string{"filename": "SKILL.md", "content": base64.StdEncoding.EncodeToString([]byte(document))})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []platform.UserRole{platform.UserRoleViewer, platform.UserRoleOperator, platform.UserRoleAdmin} {
		response := rbacRequest(t, rbacHandler(role), http.MethodPost, "/api/skills/import-preview", string(body))
		if role == platform.UserRoleViewer {
			if response.Code != http.StatusForbidden {
				t.Fatalf("viewer preview = %d", response.Code)
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("preview status=%d body=%s", response.Code, response.Body)
		}
		var result struct {
			Skill skillimport.Draft `json:"skill"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Skill.Name != "test-skill" || result.Skill.Spec.Instructions != document || result.Skill.Spec.Path != "SKILL.md" {
			t.Fatalf("preview lost imported content: %#v", result)
		}
	}
}

func TestSkillImportPreviewRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"filename":"SKILL.md","content":"invalid-base64"}`,
		`{"url":"https://example.com/SKILL.md","filename":"SKILL.md","content":"eA=="}`,
		`{"url":"https://127.0.0.1/SKILL.md"}`, `{"unknown":true}`,
		`{"filename":"SKILL.md","content":"eA=="} {}`,
	} {
		response := rbacRequest(t, rbacHandler(platform.UserRoleOperator), http.MethodPost, "/api/skills/import-preview", body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid preview request accepted: %d %s", response.Code, response.Body)
		}
	}
	body := `{"filename":"SKILL.md","content":"` + strings.Repeat("A", 8<<20) + `"}`
	response := rbacRequest(t, rbacHandler(platform.UserRoleOperator), http.MethodPost, "/api/skills/import-preview", body)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large preview request status = %d", response.Code)
	}
}
