package skillimport

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

const testDocument = "---\nname: sample-skill\ndescription: |\n  Read project references.\nlicense: MIT\nallowed-tools: Read\nmetadata:\n  version: '2.1'\n---\n\n# Instructions\nRead references/guide.md.\n"

type archiveEntry struct {
	name string
	data []byte
	mode fs.FileMode
}

func archiveBytes(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		mode := entry.mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestParsePreservesCompleteDocument(t *testing.T) {
	for _, document := range []string{testDocument, "\ufeff" + strings.ReplaceAll(testDocument, "\n", "\r\n")} {
		draft, err := Parse("SKILL.md", []byte(document))
		if err != nil {
			t.Fatal(err)
		}
		if draft.Name != "sample-skill" || draft.Description != "Read project references." ||
			draft.Spec.Version != "2.1" || draft.Spec.Instructions != testDocument || draft.Spec.Source != "upload" {
			t.Fatalf("unexpected draft: %#v", draft)
		}
	}
}

func TestParseZIPKeepsScriptAndBinaryAsset(t *testing.T) {
	binary := []byte{0, 1, 255, 254, 0, 13, 10}
	archive := archiveBytes(t,
		archiveEntry{name: "repo-main/README.md", data: []byte("outside the skill")},
		archiveEntry{name: "repo-main/skills/sample/SKILL.md", data: []byte(testDocument)},
		archiveEntry{name: "repo-main/skills/sample/scripts/check.sh", data: []byte("#!/bin/sh\nprintf ok\n"), mode: 0755},
		archiveEntry{name: "repo-main/skills/sample/assets/example.bin", data: binary},
	)
	draft, err := Parse("sample.zip", archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Spec.Files) != 2 || draft.Spec.Instructions != testDocument {
		t.Fatalf("unexpected files: %#v", draft.Spec)
	}
	asset, err := base64.StdEncoding.DecodeString(draft.Spec.Files[0].Content)
	if err != nil || !bytes.Equal(asset, binary) || draft.Spec.Files[0].Path != "assets/example.bin" ||
		draft.Spec.Files[1].Path != "scripts/check.sh" || !draft.Spec.Files[1].Executable {
		t.Fatalf("attachment bytes or mode lost: %#v", draft.Spec.Files)
	}
}

func TestParseRejectsInvalidDocuments(t *testing.T) {
	for name, document := range map[string][]byte{
		"missing frontmatter": []byte("# just markdown"),
		"missing description": []byte("---\nname: sample\n---\nDo something"),
		"invalid YAML":        []byte("---\nname: [oops\ndescription: test\n---\nDo something"),
		"empty instructions":  []byte("---\nname: sample\ndescription: test\n---\n"),
		"invalid UTF8":        {0xff, 0xfe},
		"NUL":                 append([]byte(testDocument), 0),
		"large document":      []byte(testDocument + strings.Repeat("a", platform.MaxSkillFileBytes)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("SKILL.md", document); !platform.IsValidationError(err) {
				t.Fatalf("invalid document accepted: %v", err)
			}
		})
	}
}

func TestParseRejectsUnsafeArchives(t *testing.T) {
	for name, entries := range map[string][]archiveEntry{
		"traversal":               {{name: "../escape.txt"}},
		"absolute":                {{name: "/escape.txt"}},
		"backslash":               {{name: `scripts\..\escape.txt`}},
		"symlink":                 {{name: "scripts/link", data: []byte("/etc"), mode: fs.ModeSymlink | 0777}},
		"duplicate":               {{name: "scripts/a.sh"}, {name: "scripts/a.sh"}},
		"document duplicate":      {{name: "SKILL.md", data: []byte(testDocument)}},
		"file directory conflict": {{name: "scripts"}, {name: "scripts/a.sh"}},
		"large file":              {{name: "asset", data: bytes.Repeat([]byte("a"), platform.MaxSkillFileBytes+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			entries = append([]archiveEntry{{name: "SKILL.md", data: []byte(testDocument)}}, entries...)
			if _, err := Parse("sample.zip", archiveBytes(t, entries...)); !platform.IsValidationError(err) {
				t.Fatalf("unsafe archive accepted: %v", err)
			}
		})
	}
	if _, err := Parse("sample.zip", archiveBytes(t,
		archiveEntry{name: "a/SKILL.md", data: []byte(testDocument)},
		archiveEntry{name: "b/SKILL.md", data: []byte(testDocument)},
	)); !platform.IsValidationError(err) {
		t.Fatalf("multiple skills accepted: %v", err)
	}
	if _, err := Parse("sample.zip", []byte("not a zip")); !platform.IsValidationError(err) {
		t.Fatalf("corrupt archive accepted: %v", err)
	}
}

func TestArchiveLimitsCountUncompressedBytesAndAllFiles(t *testing.T) {
	var entries []archiveEntry
	for i := range platform.MaxSkillFiles - 1 {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("references/%d.md", i)})
	}
	entries = append(entries, archiveEntry{name: "SKILL.md", data: []byte(testDocument)})
	if _, err := Parse("sample.zip", archiveBytes(t, entries...)); err != nil {
		t.Fatalf("128 files with SKILL.md last must be accepted: %v", err)
	}
	entries = append(entries, archiveEntry{name: "extra.txt"})
	if _, err := Parse("sample.zip", archiveBytes(t, entries...)); !platform.IsValidationError(err) {
		t.Fatalf("129 files accepted: %v", err)
	}
	entries = []archiveEntry{{name: "SKILL.md", data: []byte(testDocument)}}
	for i := range 4 {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("assets/%d", i), data: bytes.Repeat([]byte("a"), platform.MaxSkillFileBytes)})
	}
	archive := archiveBytes(t, entries...)
	if len(archive) >= platform.MaxSkillBundleBytes {
		t.Fatal("test needs a small compressed archive")
	}
	if _, err := Parse("sample.zip", archive); !platform.IsValidationError(err) {
		t.Fatalf("decompression size limit not enforced: %v", err)
	}
}
