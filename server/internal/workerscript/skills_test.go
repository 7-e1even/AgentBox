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

func TestSkillManagedSetReconcilesSelectedAgents(t *testing.T) {
	sh := managedConfigShell(t)
	installation := daemonSection(t, "managed_paths_reconcile() {", "read_container_file() {")
	root := t.TempDir()
	jobPath := filepath.Join(t.TempDir(), "job.json")
	document := "---\nname: qa-skill\ndescription: Imported skill\nlicense: MIT\n---\n\nRead references/guide.md.\n"
	binary := []byte{0, 255, 254, 1, 0, 13, 10}
	qaSkill := func(files []platform.SkillFile) map[string]any {
		return map[string]any{"id": "qa-skill", "name": "Catalog display name", "description": "Display description",
			"spec": platform.SkillSpec{Instructions: document, Files: files}}
	}
	legacySkill := map[string]any{"id": "legacy-skill", "name": "Legacy Skill", "description": "Legacy description",
		"spec": platform.SkillSpec{Instructions: "Legacy inline instructions"}}
	writeJob := func(skills []map[string]any, agentTools []string) {
		t.Helper()
		job, err := json.Marshal(map[string]any{"job": map[string]any{"payload": map[string]any{
			"skills": skills, "agentTools": agentTools,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jobPath, job, 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := []platform.SkillFile{
		{Path: "scripts/check.sh", Content: base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nprintf ok\n")), Executable: true},
		{Path: "assets/sample.bin", Content: base64.StdEncoding.EncodeToString(binary)},
	}
	writeJob([]map[string]any{qaSkill(files), legacySkill}, []string{"codex", "claude-code"})
	mock := `
map_guest_path() {
  case "$1" in
    /opt/*|/root/*) printf '%s%s' "$TEST_ROOT" "$1" ;;
    *) printf '%s' "$1" ;;
  esac
}
docker() {
  if [ -n "${FAIL_AT:-}" ]; then case "$*" in *"$FAIL_AT"*) return 19 ;; esac; fi
  [ "$1" = exec ] || return 91
  shift
  [ "$1" != -i ] || shift
  [ "$1" = fixture ] || return 92
  shift
  case "$1" in
    sh)
      [ "$2" = -c ] || return 93
      GUEST_SCRIPT=$(printf '%s' "$3" | sed -e "s|/opt/agentbox|$TEST_ROOT/opt/agentbox|g" -e "s|/root/|$TEST_ROOT/root/|g")
      shift 3
      GUEST_NAME=sh
      if [ "$#" -gt 0 ]; then GUEST_NAME=$1; shift; fi
      GUEST_ARGUMENTS=
      for GUEST_ARGUMENT do
        GUEST_ARGUMENT=$(map_guest_path "$GUEST_ARGUMENT")
        GUEST_ARGUMENTS="$GUEST_ARGUMENTS${GUEST_ARGUMENTS:+
}$GUEST_ARGUMENT"
      done
      GUEST_OLD_IFS=$IFS
      IFS='
'
      set -- $GUEST_ARGUMENTS
      IFS=$GUEST_OLD_IFS
      sh -c "$GUEST_SCRIPT" "$GUEST_NAME" "$@"
      ;;
    mkdir)
      [ "$2" = -p ] || return 94
      mkdir -p "$(map_guest_path "$3")"
      ;;
    chmod) chmod "$2" "$(map_guest_path "$3")" ;;
    rm)
      [ "$2" = -rf ] || return 95
      rm -rf "$(map_guest_path "$3")"
      ;;
    *) return 96 ;;
  esac
}
fixture_managed_paths_reconcile() {
  while IFS="$(printf '\t')" read -r ACTION TARGET SOURCE REPLACE MODE; do
    [ -n "$ACTION" ] || continue
    TARGET=$(map_guest_path "$TARGET")
    SOURCE=$(map_guest_path "$SOURCE")
    if [ "$ACTION" = delete ]; then
      rm -rf "$TARGET"
      continue
    fi
    PARENT=${TARGET%/*}
    mkdir -p "$PARENT"
    NEXT="$PARENT/.fixture-next-${TARGET##*/}"
    rm -rf "$NEXT"
    if [ -d "$SOURCE" ]; then mkdir "$NEXT"; cp -R "$SOURCE/." "$NEXT/"; else cp "$SOURCE" "$NEXT"; fi
    [ "$MODE" = - ] || chmod "$MODE" "$NEXT"
    rm -rf "$TARGET"
    mv "$NEXT" "$TARGET"
  done < "$2"
}
`
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	run := func(failure string) ([]byte, error) {
		command := exec.CommandContext(t.Context(), sh, "-s")
		command.Env = append(command.Environ(), "TEST_ROOT="+shellPath(root),
			"TEST_JOB="+shellPath(jobPath), "FAIL_AT="+failure, "TMPDIR="+shellPath(t.TempDir()))
		command.Stdin = strings.NewReader("set -eu\n" + mock + installation + `
managed_paths_reconcile() { fixture_managed_paths_reconcile "$@"; }
configure_skills fixture "$TEST_JOB"
`)
		return command.CombinedOutput()
	}
	if output, err := run(""); err != nil {
		t.Fatalf("initial Skill reconciliation failed: %v\n%s", err, output)
	}
	for _, target := range []string{"opt/agentbox/skills", "root/.agents/skills", "root/.claude/skills"} {
		main, err := os.ReadFile(filepath.Join(root, target, "qa-skill", "SKILL.md"))
		if err != nil || string(main) != document {
			t.Fatalf("%s lost Skill metadata: %q, %v", target, main, err)
		}
		asset, err := os.ReadFile(filepath.Join(root, target, "qa-skill", "assets", "sample.bin"))
		if err != nil || !bytes.Equal(asset, binary) {
			t.Fatalf("%s lost binary attachment: %v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "root/.codex/skills/qa-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex Skill was copied to deprecated ~/.codex/skills: %v", err)
	}
	if output, err := run(""); err != nil {
		t.Fatalf("idempotent Skill restart failed: %v\n%s", err, output)
	}
	if output, err := run("assets/sample.bin"); err == nil {
		t.Fatalf("injected staging failure succeeded: %s", output)
	}
	if asset, err := os.ReadFile(filepath.Join(root, "root/.agents/skills/qa-skill/assets/sample.bin")); err != nil || !bytes.Equal(asset, binary) {
		t.Fatalf("staging failure changed the active Skill: %v", err)
	}

	writeJob([]map[string]any{qaSkill(nil)}, []string{"codex"})
	if output, err := run(""); err != nil {
		t.Fatalf("Skill update/delete reconciliation failed: %v\n%s", err, output)
	}
	for _, path := range []string{
		"root/.agents/skills/legacy-skill", "root/.claude/skills/legacy-skill", "root/.claude/skills/qa-skill",
		"root/.agents/skills/qa-skill/assets/sample.bin",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed managed Skill path survived: %s (%v)", path, err)
		}
	}

	writeJob(nil, []string{"codex"})
	if output, err := run(""); err != nil {
		t.Fatalf("Skill clear reconciliation failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "root/.agents/skills/qa-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleared Skill survived: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "opt/agentbox/skills.manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"ids": []`) {
		t.Fatalf("cleared Skill manifest = %s, %v", manifest, err)
	}
}

func TestSkillWorkerRejectsInvalidIDsAndAttachmentPaths(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	validFile := platform.SkillFile{Path: "references/guide.md", Content: base64.StdEncoding.EncodeToString([]byte("ok"))}
	for _, test := range []struct {
		name  string
		id    string
		files []platform.SkillFile
	}{
		{"leading hyphen id", "-x", []platform.SkillFile{validFile}},
		{"double hyphen id", "x--y", []platform.SkillFile{validFile}},
		{"short id", "x", []platform.SkillFile{validFile}},
		{"long id", strings.Repeat("x", 65), []platform.SkillFile{validFile}},
		{"tab path", "qa-skill", []platform.SkillFile{{Path: "references/evil\tname", Content: "eA=="}}},
		{"newline path", "qa-skill", []platform.SkillFile{{Path: "references/evil\nname", Content: "eA=="}}},
		{"carriage return path", "qa-skill", []platform.SkillFile{{Path: "references/evil\rname", Content: "eA=="}}},
		{"NUL path", "qa-skill", []platform.SkillFile{{Path: "references/evil\x00name", Content: "eA=="}}},
		{"long path", "qa-skill", []platform.SkillFile{{Path: strings.Repeat("x", 241), Content: "eA=="}}},
		{"empty component", "qa-skill", []platform.SkillFile{{Path: "references//guide.md", Content: "eA=="}}},
		{"parent component", "qa-skill", []platform.SkillFile{{Path: "references/../guide.md", Content: "eA=="}}},
		{"case duplicate", "qa-skill", []platform.SkillFile{{Path: "Guide.md", Content: "eA=="}, {Path: "guide.md", Content: "eQ=="}}},
		{"file directory conflict", "qa-skill", []platform.SkillFile{{Path: "references", Content: "eA=="}, {Path: "references/guide.md", Content: "eQ=="}}},
		{"main document collision", "qa-skill", []platform.SkillFile{{Path: "skill.MD", Content: "eA=="}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			job := filepath.Join(t.TempDir(), "job.json")
			secret := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(secret, nil, 0600); err != nil {
				t.Fatal(err)
			}
			writeManagedJob(t, job, map[string]any{
				"skills": []map[string]any{{"id": test.id, "name": "QA", "description": "QA skill", "spec": platform.SkillSpec{
					Instructions: "instructions", Files: test.files,
				}}},
				"agentTools": []string{"codex"},
			})
			output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_skills fixture "$TEST_JOB"`)
			if err == nil {
				t.Fatalf("invalid Skill definition succeeded: %s", output)
			}
			if _, statErr := os.Stat(filepath.Join(root, "root/.agents/skills/qa-skill")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid Skill definition changed active paths: %v", statErr)
			}
		})
	}
}
