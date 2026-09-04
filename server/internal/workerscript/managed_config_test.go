package workerscript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func managedConfigShell(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not available")
	}
	return sh
}

func shellPath(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(path) >= 3 && path[1] == ':' {
		return "/" + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func daemonSection(t *testing.T, start, end string) string {
	t.Helper()
	_, section, found := strings.Cut(workerDaemon, "\n"+start)
	if !found {
		t.Fatalf("Worker function %q is missing", start)
	}
	section, _, found = strings.Cut(section, "\n"+end)
	if !found {
		t.Fatalf("Worker function boundary %q is missing", end)
	}
	return start + section
}

func TestManagedPathTransactionRollsBackEveryAppliedTarget(t *testing.T) {
	sh := managedConfigShell(t)
	function := daemonSection(t, "managed_paths_reconcile() {", "skill_target_for_tool() {")
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	targetOne := filepath.Join(root, "one.json")
	targetTwo := filepath.Join(root, "two.json")
	sourceOne := filepath.Join(root, "new-one.json")
	sourceTwo := filepath.Join(root, "new-two.json")
	for path, content := range map[string]string{
		targetOne: "old-one", targetTwo: "old-two", sourceOne: "new-one", sourceTwo: "new-two",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	operations := filepath.Join(root, "operations")
	writeOperations := func() {
		t.Helper()
		content := "put\t" + shellPath(targetOne) + "\t" + shellPath(sourceOne) + "\t1\t600\n" +
			"put\t" + shellPath(targetTwo) + "\t" + shellPath(sourceTwo) + "\t1\t600\n"
		if err := os.WriteFile(operations, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeOperations()
	mock := `
docker() {
  [ "$1" = exec ] || return 91
  shift
  [ "$1" != -i ] || shift
  [ "$1" = fixture ] || return 92
  shift
  [ "$1" = sh ] && [ "$2" = -c ] || return 93
  GUEST_SCRIPT=$3
  shift 3
  [ "$1" = agentbox ] || return 94
  shift
  GUEST_STATE=$1
  [ "$GUEST_STATE" != /opt/agentbox ] || GUEST_STATE=$TEST_STATE
  GUEST_SCRIPT='mv() {
    MV_LAST=
    for MV_ARGUMENT do MV_LAST=$MV_ARGUMENT; done
    if [ -n "${FAIL_TARGET:-}" ] && [ "$MV_LAST" = "$FAIL_TARGET" ]; then
      case "$1" in *.agentbox-next-*) return 19 ;; esac
    fi
    command mv "$@"
  }
'"$GUEST_SCRIPT"
  sh -c "$GUEST_SCRIPT" agentbox "$GUEST_STATE"
}
`
	run := func(failTarget string) ([]byte, error) {
		command := exec.CommandContext(t.Context(), sh, "-s")
		command.Env = append(command.Environ(),
			"TEST_STATE="+shellPath(state), "TEST_OPERATIONS="+shellPath(operations), "FAIL_TARGET="+failTarget)
		command.Stdin = strings.NewReader("set -eu\n" + mock + function + `
managed_paths_reconcile fixture "$TEST_OPERATIONS"
`)
		return command.CombinedOutput()
	}
	if output, err := run(shellPath(targetTwo)); err == nil {
		t.Fatalf("injected second-target failure succeeded: %s", output)
	}
	for path, want := range map[string]string{targetOne: "old-one", targetTwo: "old-two"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("rollback %s = %q, %v; want %q", path, got, err, want)
		}
	}
	writeOperations()
	if output, err := run(""); err != nil {
		t.Fatalf("managed transaction failed: %v\n%s", err, output)
	}
	for path, want := range map[string]string{targetOne: "new-one", targetTwo: "new-two"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("commit %s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestManagedPathTransactionRejectsSymlinkedTargetAndSourceChains(t *testing.T) {
	sh := managedConfigShell(t)
	function := daemonSection(t, "managed_paths_reconcile() {", "skill_target_for_tool() {")
	mock := `
docker() {
  [ "$1" = exec ] || return 91
  shift
  [ "$1" != -i ] || shift
  [ "$1" = fixture ] || return 92
  shift
  [ "$1" = sh ] && [ "$2" = -c ] || return 93
  GUEST_SCRIPT=$3
  shift 3
  [ "$1" = agentbox ] || return 94
  shift
  GUEST_STATE=$1
  [ "$GUEST_STATE" != /opt/agentbox ] || GUEST_STATE=$TEST_STATE
  sh -c "$GUEST_SCRIPT" agentbox "$GUEST_STATE"
}
`
	for _, sourceChain := range []bool{false, true} {
		name := "target parent"
		if sourceChain {
			name = "source parent"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			outside := filepath.Join(root, "outside")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "linked")
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("directory symlinks are unavailable: %v", err)
			}
			target := filepath.Join(root, "safe", "managed.json")
			source := filepath.Join(root, "source.json")
			if sourceChain {
				source = filepath.Join(link, "source.json")
				target = filepath.Join(root, "safe", "managed.json")
			} else {
				target = filepath.Join(link, "managed.json")
			}
			if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			operations := filepath.Join(root, "operations")
			content := "put\t" + shellPath(target) + "\t" + shellPath(source) + "\t1\t600\n"
			if err := os.WriteFile(operations, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATE="+shellPath(state), "TEST_OPERATIONS="+shellPath(operations))
			command.Stdin = strings.NewReader("set -eu\n" + mock + function + `
managed_paths_reconcile fixture "$TEST_OPERATIONS"
`)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("symlinked managed path was accepted: (%q, %v)", output, err)
			}
			if !strings.Contains(string(output), "symlink") {
				t.Fatalf("symlinked managed path returned an ambiguous error: (%q, %v)", output, err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("symlinked managed path changed target: %v", err)
			}
		})
	}
	for _, control := range []string{"\t", "\n", "\r", "\x7f"} {
		t.Run("target control", func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source.json")
			if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "bad"+control+"path", "managed.json")
			operations := filepath.Join(root, "operations")
			content := "put\t" + shellPath(target) + "\t" + shellPath(source) + "\t1\t600\n"
			if err := os.WriteFile(operations, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATE="+shellPath(state), "TEST_OPERATIONS="+shellPath(operations))
			command.Stdin = strings.NewReader("set -eu\n" + mock + function + `
managed_paths_reconcile fixture "$TEST_OPERATIONS"
`)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("control-bearing managed target was accepted: %q", output)
			}
			if _, err := os.Stat(target); err == nil {
				t.Fatalf("control-bearing managed target changed active path: %v", err)
			}
		})
	}
	for _, control := range []string{"\t", "\n", "\r", "\x7f"} {
		t.Run("source control", func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "managed.json")
			source := filepath.Join(root, "bad"+control+"source.json")
			operations := filepath.Join(root, "operations")
			content := "put\t" + shellPath(target) + "\t" + shellPath(source) + "\t1\t600\n"
			if err := os.WriteFile(operations, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATE="+shellPath(state), "TEST_OPERATIONS="+shellPath(operations))
			command.Stdin = strings.NewReader("set -eu\n" + mock + function + `
managed_paths_reconcile fixture "$TEST_OPERATIONS"
`)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("control-bearing managed source was accepted: %q", output)
			}
			if _, err := os.Stat(target); err == nil {
				t.Fatalf("control-bearing managed source changed active path: %v", err)
			}
		})
	}
}

func TestSandboxManifestUsesANonSecretAllowlist(t *testing.T) {
	sh := managedConfigShell(t)
	function := daemonSection(t, "build_sandbox_manifest() {", "create_sandbox() {")
	root := t.TempDir()
	job := filepath.Join(root, "job.json")
	manifest := filepath.Join(root, "manifest.json")
	const marker = "must-never-enter-sandbox-manifest"
	writeManagedJob(t, job, map[string]any{
		"sandboxId": "sandbox-one", "name": "Sandbox", "driver": "docker", "image": "runtime:latest",
		"workdir": "/workspace", "cpu": "2", "memory": "4GiB", "desktop": true, "network": "egress",
		"workspace": "https://example.invalid/repository.git", "proxyId": "proxy-one",
		"agentTools": []string{"codex"}, "credentialIds": []string{"credential-one"},
		"skillIds": []string{"skill-one"}, "mcpServerIds": []string{"mcp-one"},
		"variableIds": []string{"variable-one"}, "extensionIds": []string{"extension-one"},
		"capabilityDigest": "sha256:fixture", "mcpAllowNet": []string{"mcp.example.invalid"},
		"controlPlane":         map[string]any{"allowNet": []string{"api.example.invalid"}, "token": marker},
		"credentials":          []map[string]any{{"id": "credential-one", "secret": marker}},
		"environmentVariables": []map[string]any{{"name": "TOKEN", "value": marker}},
		"proxy":                map[string]any{"url": "https://user:" + marker + "@proxy.invalid"},
		"setup":                "printf '" + marker + "'",
		"modelBindings":        map[string]any{"codex": marker},
		"futureSecret":         marker,
		"extensions": []map[string]any{{"id": "extension-one", "spec": map[string]any{
			"installScript": marker, "verifyScript": marker,
		}}},
		"variables": []map[string]any{{"id": "variable-one", "spec": map[string]any{
			"key": "TARGET_TOKEN", "mode": "secret-ref", "reference": "secret://HOST_SECRET_FILE",
		}}},
		"skills": []map[string]any{{"id": "skill-one", "name": "Skill", "spec": map[string]any{
			"bundleDigest": "sha256:skill", "instructions": marker,
			"files": []map[string]any{{"path": "secret.txt", "content": marker}},
		}}},
		"mcpServers": []map[string]any{{"id": "mcp-one", "name": "MCP", "spec": map[string]any{
			"transport": "http", "url": "https://mcp.example.invalid", "headers": []map[string]any{{
				"name": "Authorization", "valueFrom": "secret://TARGET_TOKEN",
			}},
		}}},
	})
	mock := ""
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n"
	}
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Env = append(command.Environ(), "TEST_JOB="+shellPath(job), "TEST_MANIFEST="+shellPath(manifest))
	command.Stdin = strings.NewReader("set -eu\n" + mock + function + `
build_sandbox_manifest "$TEST_JOB" "$TEST_MANIFEST"
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sandbox manifest: %v\n%s", err, output)
	}
	encoded, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{marker, "HOST_SECRET_FILE", "secret://", "environmentVariables", "modelBindings", "futureSecret", "setup", "headers", "installScript"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("sandbox manifest contains forbidden %q: %s", forbidden, encoded)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, forbiddenKey := range []string{"credentials", "extensions", "proxy"} {
		if _, exists := decoded[forbiddenKey]; exists {
			t.Fatalf("sandbox manifest contains forbidden key %q: %#v", forbiddenKey, decoded)
		}
	}
	variables := decoded["variables"].([]any)
	variable := variables[0].(map[string]any)
	if variable["id"] != "variable-one" || variable["key"] != "TARGET_TOKEN" || len(variable) != 2 {
		t.Fatalf("Variable summary = %#v", variable)
	}
	skills := decoded["skills"].([]any)
	skill := skills[0].(map[string]any)
	if skill["digest"] != "sha256:skill" || skill["fileCount"] != float64(2) {
		t.Fatalf("Skill summary = %#v", skill)
	}
	mcpServers := decoded["mcpServers"].([]any)
	mcp := mcpServers[0].(map[string]any)
	if mcp["transport"] != "http" || len(mcp) != 3 {
		t.Fatalf("MCP summary = %#v", mcp)
	}
}

func TestManagedConfigurationResolvesVariablesOncePerJob(t *testing.T) {
	sh := managedConfigShell(t)
	function := daemonSection(t, "configure_sandbox_agent_config() (", "install_agent_wrappers() {")
	root := t.TempDir()
	job := filepath.Join(root, "job.json")
	count := filepath.Join(root, "resolve-count")
	variables := filepath.Join(root, "variables.json")
	mcp := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(job, []byte(`{"job":{"payload":{"image":"runtime:latest"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	mock := `
resolve_worker_variables() {
  COUNT=0
  [ ! -f "$TEST_COUNT" ] || COUNT=$(cat "$TEST_COUNT")
  COUNT=$((COUNT + 1))
  printf '%s' "$COUNT" > "$TEST_COUNT"
  printf '{"TOKEN":"snapshot-%s"}\n' "$COUNT" > "$2"
}
configure_credentials() { :; }
configure_variables() { cp "$3" "$TEST_VARIABLES"; }
configure_skills() { :; }
configure_mcp_servers() { cp "$3" "$TEST_MCP"; }
install_agent_wrappers() { :; }
`
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Env = append(command.Environ(), "TEST_JOB="+shellPath(job), "TEST_COUNT="+shellPath(count),
		"TEST_VARIABLES="+shellPath(variables), "TEST_MCP="+shellPath(mcp), "TMPDIR="+shellPath(t.TempDir()))
	command.Stdin = strings.NewReader("set -eu\n" + function + mock + `
configure_sandbox_agent_config fixture "$TEST_JOB"
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("configure sandbox Agent resources: %v\n%s", err, output)
	}
	resolvedCount, err := os.ReadFile(count)
	if err != nil || string(resolvedCount) != "1" {
		t.Fatalf("Variable resolver count = %q, %v", resolvedCount, err)
	}
	variableSnapshot, err := os.ReadFile(variables)
	if err != nil {
		t.Fatal(err)
	}
	mcpSnapshot, err := os.ReadFile(mcp)
	if err != nil || string(variableSnapshot) != string(mcpSnapshot) || !strings.Contains(string(mcpSnapshot), "snapshot-1") {
		t.Fatalf("Variable/MCP snapshots differ: variables=%q mcp=%q err=%v", variableSnapshot, mcpSnapshot, err)
	}
}
