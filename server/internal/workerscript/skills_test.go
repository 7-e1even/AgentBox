package workerscript

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestSkillInstallationPreservesFilesAndPropagatesFailures(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not available")
	}
	_, installation, found := strings.Cut(workerDaemon, "\nconfigure_skills() {")
	if !found {
		t.Fatal("Skill installation function missing")
	}
	installation, _, found = strings.Cut(installation, "\nconfigure_mcp_servers() {")
	if !found {
		t.Fatal("Skill installation boundary missing")
	}
	installation = "configure_skills() {" + installation
	root := t.TempDir()
	jobPath := filepath.Join(t.TempDir(), "job.json")
	document := "---\nname: qa-skill\ndescription: Imported skill\nlicense: MIT\n---\n\nRead references/guide.md.\n"
	binary := []byte{0, 255, 254, 1, 0, 13, 10}
	writeJob := func(files []platform.SkillFile) {
		t.Helper()
		job, err := json.Marshal(map[string]any{"job": map[string]any{"payload": map[string]any{"skills": []map[string]any{
			{"id": "qa-skill", "name": "Catalog display name", "description": "Display description", "spec": platform.SkillSpec{Instructions: document, Files: files}},
			{"id": "legacy-skill", "name": "Legacy Skill", "description": "Legacy description", "spec": platform.SkillSpec{Instructions: "Legacy inline instructions"}},
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jobPath, job, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJob([]platform.SkillFile{
		{Path: "scripts/check.sh", Content: base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nprintf ok\n")), Executable: true},
		{Path: "assets/sample.bin", Content: base64.StdEncoding.EncodeToString(binary)},
	})
	// Execute the real Worker function and actual guest filesystem commands, but
	// map the Docker transport into a disposable directory instead of a sandbox.
	mock := `
docker() {
  if [ -n "$FAIL_AT" ]; then
    case "$*" in *"$FAIL_AT"*) return 19 ;; esac
  fi
  [ "$1" = exec ] || return 1
  shift
  if [ "$1" = -i ]; then shift; fi
  [ "$1" = fixture ] || return 1
  shift
  case "$1" in
    mkdir) mkdir -p "$TEST_ROOT$3" ;;
    chmod) chmod "$2" "$TEST_ROOT$3" ;;
    sh) sh -c "$3" "$4" "$TEST_ROOT$5" "$TEST_ROOT${6-}" ;;
    *) return 1 ;;
  esac
}
`
	if runtime.GOOS == "windows" {
		// Native jq otherwise writes CRLF, unlike jq inside the Linux Worker.
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	run := func(failure string) ([]byte, error) {
		command := exec.CommandContext(t.Context(), sh, "-s")
		command.Env = append(command.Environ(), "TEST_ROOT="+filepath.ToSlash(root),
			"TEST_JOB="+filepath.ToSlash(jobPath), "FAIL_AT="+failure)
		command.Stdin = strings.NewReader("set -eu\n" + mock + installation + `
if result=$(configure_skills fixture "$TEST_JOB"); then
  printf skill-install-ok
else
  exit 1
fi
`)
		return command.CombinedOutput()
	}
	if output, err := run(""); err != nil || string(output) != "skill-install-ok" {
		t.Fatalf("installation failed: %v\n%s", err, output)
	}
	for _, target := range []string{"opt/agentbox/skills", "root/.agents/skills", "root/.codex/skills", "root/.claude/skills", "root/.pi/agent/skills"} {
		main, err := os.ReadFile(filepath.Join(root, target, "qa-skill", "SKILL.md"))
		if err != nil || string(main) != document {
			t.Fatalf("%s lost metadata or duplicated frontmatter: %q, %v", target, main, err)
		}
		asset, err := os.ReadFile(filepath.Join(root, target, "qa-skill", "assets", "sample.bin"))
		if err != nil || !bytes.Equal(asset, binary) {
			t.Fatalf("%s lost binary asset: %v", target, err)
		}
		info, err := os.Stat(filepath.Join(root, target, "qa-skill", "scripts", "check.sh"))
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) {
			t.Fatalf("%s lost executable script: %v", target, err)
		}
	}
	legacy, err := os.ReadFile(filepath.Join(root, "root/.codex/skills/legacy-skill/SKILL.md"))
	if err != nil || !strings.Contains(string(legacy), "name: \"Legacy Skill\"") || !strings.Contains(string(legacy), "Legacy inline instructions") {
		t.Fatalf("legacy skill broken: %q, %v", legacy, err)
	}
	for _, failure := range []string{"assets/sample.bin", "/root/.codex/skills/qa-skill"} {
		if output, err := run(failure); err == nil || strings.Contains(string(output), "skill-install-ok") {
			t.Fatalf("failure %s reported success: %v %s", failure, err, output)
		}
	}
	writeJob(nil)
	if output, err := run(""); err != nil {
		t.Fatalf("restart failed: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "root/.codex/skills/qa-skill/assets/sample.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed attachment survived reinstall: %v", err)
	}
}
