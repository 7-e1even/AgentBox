package skillimport

import (
	"bytes"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestSkillsSHResolvesMetadataNameAndKeepsOnlySelectedDirectory(t *testing.T) {
	document := strings.ReplaceAll(testDocument, "sample-skill", "vercel-react-best-practices")
	archive := archiveBytes(t,
		archiveEntry{name: "repo-main/skills/find-skills/SKILL.md", data: []byte(testDocument)},
		archiveEntry{name: "repo-main/skills/react-best-practices/SKILL.md", data: []byte(document)},
		archiveEntry{name: "repo-main/skills/react-best-practices/references/guide.md", data: []byte("Reference content")},
		archiveEntry{name: "repo-main/skills/react-best-practices/scripts/check.sh", data: []byte("#!/bin/sh\ntrue\n"), mode: 0755},
		archiveEntry{name: "repo-main/README.md", data: []byte("Not part of the skill")},
	)
	for _, hostname := range []string{"skills.sh", "www.skills.sh"} {
		source, _ := url.Parse("https://" + hostname + "/vercel-labs/agent-skills/vercel-react-best-practices")
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.URL.String() != "https://codeload.github.com/vercel-labs/agent-skills/zip/HEAD" {
				t.Fatalf("unexpected download: %s", request.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(archive)), Request: request}, nil
		})}
		draft, err := fetchSkillsSH(t.Context(), client, source)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || draft.Name != "vercel-react-best-practices" || draft.Spec.Source != "skills.sh" ||
			draft.Spec.Path != source.String() || draft.Spec.Instructions != document || len(draft.Spec.Files) != 2 {
			t.Fatalf("unexpected imported skill: %#v", draft)
		}
		if draft.Spec.Files[0].Content != base64.StdEncoding.EncodeToString([]byte("Reference content")) || !draft.Spec.Files[1].Executable {
			t.Fatalf("attachments were not preserved: %#v", draft.Spec.Files)
		}
	}
}

func TestSkillsSHRejectsUnsupportedLinksBeforeDownloading(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("invalid source caused a download: %s", request.URL)
		return nil, nil
	})}
	for _, link := range []string{
		"https://skills.sh/", "https://skills.sh/owner/repo", "https://skills.sh/uizze.com/anti-ui-slop",
		"https://skills.sh/../repo/sample", "https://skills.sh/owner/repo/sample/extra",
		"https://skills.sh/owner/repo/%2e%2e", "https://skills.sh/owner/repo%5cother/sample",
	} {
		source, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fetchSkillsSH(t.Context(), client, source); !platform.IsValidationError(err) {
			t.Errorf("unsupported source accepted: %s: %v", source, err)
		}
	}
}

func TestRepositorySkillRejectsAmbiguousMissingAndUnsafeContent(t *testing.T) {
	for name, entries := range map[string][]archiveEntry{
		"ambiguous": {
			{name: "repo/a/sample-skill/SKILL.md", data: []byte(testDocument)},
			{name: "repo/b/sample-skill/SKILL.md", data: []byte(testDocument)},
		},
		"missing": {{name: "repo/other/SKILL.md", data: []byte(strings.ReplaceAll(testDocument, "sample-skill", "other"))}},
		"symlink": {
			{name: "repo/sample-skill/SKILL.md", data: []byte(testDocument)},
			{name: "repo/sample-skill/scripts/link", data: []byte("/etc"), mode: fs.ModeSymlink | 0777},
		},
		"traversal": {
			{name: "repo/sample-skill/SKILL.md", data: []byte(testDocument)},
			{name: "repo/sample-skill/../outside", data: []byte("outside")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := repositorySkill(archiveBytes(t, entries...), "sample-skill"); !platform.IsValidationError(err) {
				t.Fatalf("invalid repository skill accepted: %v", err)
			}
		})
	}
}
