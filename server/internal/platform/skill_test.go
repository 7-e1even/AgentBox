package platform

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSkillResourceWritesCannotBypassImportValidation(t *testing.T) {
	project := "default"
	for name, files := range map[string][]SkillFile{
		"traversal":           {{Path: "../outside"}},
		"absolute":            {{Path: "/root/outside"}},
		"main document":       {{Path: "SKILL.md"}},
		"duplicate":           {{Path: "a"}, {Path: "A"}},
		"nested beneath file": {{Path: "scripts/check"}, {Path: "scripts"}},
		"base64":              {{Path: "asset", Content: "bad-base64"}},
		"large":               {{Path: "asset", Content: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", MaxSkillFileBytes+1)))}},
	} {
		t.Run(name, func(t *testing.T) {
			input := Input{ID: "test-skill", Kind: KindSkill, ProjectID: &project, Name: "Test skill", Spec: map[string]any{"files": files}}
			if err := Validate(input); !IsValidationError(err) {
				t.Fatalf("direct resource write accepted unsafe attachments: %v", err)
			}
		})
	}
	input := Input{ID: "test-skill", Kind: KindSkill, ProjectID: &project, Name: "Test skill", Spec: map[string]any{"instructions": "Legacy inline instructions"}}
	if err := Validate(input); err != nil {
		t.Fatalf("legacy inline skill rejected: %v", err)
	}
}
