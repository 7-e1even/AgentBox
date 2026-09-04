package platform

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestSkillResourceWritesCannotBypassImportValidation(t *testing.T) {
	project := "default"
	document := "---\nname: test-skill\ndescription: Test skill\n---\n\nDo the work.\n"
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
			input := Input{ID: "test-skill", Kind: KindSkill, ProjectID: &project, Name: "Test skill", Description: "Test skill", Spec: map[string]any{"instructions": document, "files": files}}
			if err := Validate(input); !IsValidationError(err) {
				t.Fatalf("direct resource write accepted unsafe attachments: %v", err)
			}
		})
	}
	input := Input{ID: "test-skill", Kind: KindSkill, ProjectID: &project, Name: "Test skill", Description: "Test skill", Spec: map[string]any{"instructions": "Legacy inline instructions"}}
	if err := Validate(input); !IsValidationError(err) {
		t.Fatalf("legacy inline skill write was not required to migrate: %v", err)
	}
}

func TestCanonicalizeSkillSpecDerivesProvenanceAndDigest(t *testing.T) {
	document := "---\r\nname: test-skill\r\ndescription: Test skill\r\nlicense: MIT\r\ncompatibility: Requires git\r\n---\r\n\r\nRun it.\r\n"
	spec := SkillSpec{
		Source:       "https://user:password@example.test/skill?token=secret#fragment",
		Path:         "catalog/skills/test-skill?variant=public#readme",
		BundleDigest: "client-controlled",
		FileCount:    99,
		DecodedBytes: 99,
		Instructions: document,
		Files: []SkillFile{
			{Path: "z.txt", Content: base64.StdEncoding.EncodeToString([]byte("z"))},
			{Path: "a.txt", Content: base64.StdEncoding.EncodeToString([]byte("a")), Executable: true},
		},
	}
	if err := CanonicalizeSkillSpec("test-skill", &spec); err != nil {
		t.Fatal(err)
	}
	if spec.License != "MIT" || spec.Compatibility != "Requires git" {
		t.Fatalf("provenance = %#v", spec)
	}
	if !strings.HasPrefix(spec.BundleDigest, "sha256:") || spec.BundleDigest == "client-controlled" {
		t.Fatalf("bundle digest = %q", spec.BundleDigest)
	}
	if strings.Contains(spec.Source, "?") || strings.Contains(spec.Source, "#") || strings.Contains(spec.Source, "user") || strings.Contains(spec.Source, "password") {
		t.Fatalf("source URL was not sanitized: %q", spec.Source)
	}
	if spec.Path != "catalog/skills/test-skill?variant=public#readme" {
		t.Fatalf("Skill catalog path was rewritten: %q", spec.Path)
	}
	if strings.Contains(spec.Instructions, "\r") {
		t.Fatal("document line endings were not canonicalized")
	}

	reordered := spec
	reordered.Files = []SkillFile{spec.Files[1], spec.Files[0]}
	digest, fileCount, decodedBytes, err := SkillBundleSummary(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if digest != spec.BundleDigest || fileCount != 3 || decodedBytes != len(spec.Instructions)+2 {
		t.Fatalf("summary = %q, %d, %d", digest, fileCount, decodedBytes)
	}
	if spec.FileCount != fileCount || spec.DecodedBytes != decodedBytes {
		t.Fatalf("persisted summary = %d/%d", spec.FileCount, spec.DecodedBytes)
	}
}

func TestSkillListSpecUsesPersistedSummaryWithoutDecodingBundle(t *testing.T) {
	summary := SkillListSpec(map[string]any{
		"bundleDigest": "sha256:server-derived",
		"fileCount":    float64(3),
		"decodedBytes": float64(42),
		"instructions": "not valid frontmatter",
		"files":        "not a bundle",
	})
	if !reflect.DeepEqual(summary["fileCount"], 3) || !reflect.DeepEqual(summary["decodedBytes"], 42) {
		t.Fatalf("persisted summary = %#v", summary)
	}
}

func TestSkillListSpecOmitsBundleContent(t *testing.T) {
	document := "---\nname: test-skill\ndescription: Test skill\n---\n\nRun it.\n"
	spec := SkillSpec{Instructions: document, Files: []SkillFile{{Path: "asset", Content: "YQ=="}}}
	if err := CanonicalizeSkillSpec("test-skill", &spec); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{
		"instructions": spec.Instructions,
		"files":        []any{map[string]any{"path": "asset", "content": "YQ=="}},
		"bundleDigest": spec.BundleDigest,
		"license":      spec.License,
	}
	summary := SkillListSpec(raw)
	if _, exists := summary["instructions"]; exists {
		t.Fatal("list summary returned instructions")
	}
	if _, exists := summary["files"]; exists {
		t.Fatal("list summary returned files")
	}
	if !reflect.DeepEqual(summary["fileCount"], 2) || !reflect.DeepEqual(summary["decodedBytes"], len(document)+1) {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestSkillResourceRequiresMatchingFrontmatterName(t *testing.T) {
	spec := SkillSpec{Instructions: "---\nname: another-skill\ndescription: Test\n---\n\nRun it.\n"}
	if err := ValidateSkillResource("test-skill", "Test", spec); !IsValidationError(err) {
		t.Fatalf("mismatched name error = %v", err)
	}
}
