package workerscript

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func managedFilesystemMock() string {
	return `
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
remove_env() {
  FILTERED=$(mktemp)
  awk -v key="$2" 'index($0, key "=") != 1' "$1" > "$FILTERED"
  cat "$FILTERED" > "$1"
  rm -f "$FILTERED"
}
`
}

func runManagedFixture(t *testing.T, functions, root, job, secret, failure, invoke string, extraEnv ...string) ([]byte, error) {
	t.Helper()
	sh := managedConfigShell(t)
	mock := managedFilesystemMock()
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Env = append(command.Environ(),
		"TEST_ROOT="+shellPath(root), "TEST_JOB="+shellPath(job), "TEST_SECRET_FILE="+shellPath(secret),
		"FAIL_AT="+failure, "TMPDIR="+shellPath(t.TempDir()))
	command.Env = append(command.Env, extraEnv...)
	command.Stdin = strings.NewReader("set -eu\n" + mock + functions + `
worker_secret_path() { printf '%s' "$TEST_SECRET_FILE"; }
worker_secret_metadata() {
  if [ -n "${TEST_SECRET_METADATA:-}" ]; then printf '%s' "$TEST_SECRET_METADATA"
  else printf '0:600:%s' "$(wc -c < "$1" | tr -d '[:space:]')"; fi
}
managed_paths_reconcile() { fixture_managed_paths_reconcile "$@"; }
` + invoke)
	return command.CombinedOutput()
}

func writeManagedJob(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"job": map[string]any{"payload": payload}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestVariableManagedSetResolvesUpdatesAndClears(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	root := t.TempDir()
	job := filepath.Join(t.TempDir(), "job.json")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret-value\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(root, "opt/agentbox/secrets/agentbox.env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("KEEP='user-owned'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	variables := []map[string]any{
		{"id": "env-token", "spec": map[string]any{"key": "ENV_TOKEN", "mode": "value-ref", "reference": "env://HOST_ENV"}},
		{"id": "multiline-token", "spec": map[string]any{"key": "MULTI_TOKEN", "mode": "value-ref", "reference": "env://HOST_MULTI"}},
		{"id": "secret-token", "spec": map[string]any{"key": "SECRET_TOKEN", "mode": "secret-ref", "reference": "secret://HOST_SECRET"}},
	}
	writeManagedJob(t, job, map[string]any{"variables": variables})
	const multilineValue = "first line\nsecond line\n"
	output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_variables fixture "$TEST_JOB"`, "HOST_ENV=first-value", "HOST_MULTI="+multilineValue)
	if err != nil {
		t.Fatalf("Variable resolution failed: %v\n%s", err, output)
	}
	env, err := os.ReadFile(envPath)
	if err != nil || !strings.Contains(string(env), "KEEP='user-owned'") ||
		!strings.Contains(string(env), "ENV_TOKEN='first-value'") || !strings.Contains(string(env), "SECRET_TOKEN='secret-value'") {
		t.Fatalf("resolved environment = %q, %v", env, err)
	}
	if strings.Count(string(env), "ENV_TOKEN=") != 1 {
		t.Fatalf("resolved environment contains duplicate key: %q", env)
	}
	resolvedMultiline := filepath.Join(t.TempDir(), "multiline")
	sh := managedConfigShell(t)
	command := exec.CommandContext(t.Context(), sh, "-c", `set -a; . "$1"; set +a; printf '%s' "$MULTI_TOKEN" > "$2"`, "agentbox", shellPath(envPath), shellPath(resolvedMultiline))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("source multiline Variable: %v\n%s\nenv=%s", err, output, env)
	}
	resolved, err := os.ReadFile(resolvedMultiline)
	if err != nil || string(resolved) != multilineValue {
		t.Fatalf("multiline Variable = %q, %v", resolved, err)
	}
	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_variables fixture "$TEST_JOB"`, "HOST_ENV=updated-value", "HOST_MULTI="+multilineValue)
	if err != nil {
		t.Fatalf("Variable update failed: %v\n%s", err, output)
	}
	env, _ = os.ReadFile(envPath)
	if strings.Contains(string(env), "first-value") || !strings.Contains(string(env), "updated-value") {
		t.Fatalf("Variable update was not reconciled: %q", env)
	}

	output, err = runManagedFixture(t, functions, root, job, secret, "manifest.json", `configure_variables fixture "$TEST_JOB"`, "HOST_ENV=must-not-commit", "HOST_MULTI="+multilineValue)
	if err == nil {
		t.Fatalf("injected Variable staging failure succeeded: %s", output)
	}
	env, _ = os.ReadFile(envPath)
	if strings.Contains(string(env), "must-not-commit") || !strings.Contains(string(env), "updated-value") {
		t.Fatalf("Variable staging failure changed active environment: %q", env)
	}

	// Simulate configure_credentials handing ENV_TOKEN back to a direct
	// environment variable before Variable reconciliation runs.
	if err := os.WriteFile(envPath, []byte("KEEP='user-owned'\nENV_TOKEN='direct-value'\nSECRET_TOKEN='secret-value'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeManagedJob(t, job, map[string]any{
		"variables":            []any{},
		"environmentVariables": []map[string]any{{"name": "ENV_TOKEN", "value": "direct-value"}},
	})
	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_variables fixture "$TEST_JOB"`)
	if err != nil {
		t.Fatalf("Variable clear failed: %v\n%s", err, output)
	}
	env, _ = os.ReadFile(envPath)
	if !strings.Contains(string(env), "ENV_TOKEN='direct-value'") || strings.Contains(string(env), "SECRET_TOKEN=") ||
		strings.Contains(string(env), "MULTI_TOKEN=") || !strings.Contains(string(env), "KEEP='user-owned'") {
		t.Fatalf("Variable clear removed the wrong keys: %q", env)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "opt/agentbox/variables.manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"keys": []`) {
		t.Fatalf("cleared Variable manifest = %s, %v", manifest, err)
	}
}

func TestMCPManagedSetReconcilesAdaptersAndHeaderReferences(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	root := t.TempDir()
	job := filepath.Join(t.TempDir(), "job.json")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("dsh-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	initialFiles := map[string]string{
		"root/.codex/config.toml":             "model = \"fixture\"\n\n[mcp_servers.user]\nurl = \"https://user.invalid\"\n",
		"root/.claude.json":                   `{"theme":"dark","mcpServers":{"user":{"type":"http","url":"https://user.invalid"}}}`,
		"root/.gemini/settings.json":          `{"theme":"dark","mcpServers":{"user":{"httpUrl":"https://user.invalid"}}}`,
		"root/.config/opencode/opencode.json": `{"theme":"dark","mcp":{"user":{"type":"remote","url":"https://user.invalid"}}}`,
	}
	for relative, content := range initialFiles {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	variables := []map[string]any{
		{"id": "auth", "spec": map[string]any{"key": "AUTH_TOKEN", "mode": "value-ref", "reference": "env://HOST_AUTH"}},
		{"id": "secret", "spec": map[string]any{"key": "SECRET_TOKEN", "mode": "secret-ref", "reference": "secret://HOST_SECRET"}},
	}
	servers := []map[string]any{
		{"id": "alpha", "name": "Alpha", "spec": map[string]any{"transport": "stdio", "command": "npx", "arguments": []string{"-y", "arg with space"}, "cwd": ""}},
		{"id": "beta", "name": "Beta", "spec": map[string]any{"transport": "http", "url": "https://beta.invalid/mcp", "headers": []map[string]string{{"name": "Authorization", "valueFrom": "env://AUTH_TOKEN"}}}},
		{"id": "legacy", "name": "Legacy", "spec": map[string]any{"transport": "http", "url": "https://legacy.invalid/mcp", "headers": "X-Secret=secret://SECRET_TOKEN"}},
		{"id": "legacy-args", "name": "Legacy Args", "spec": map[string]any{"transport": "stdio", "command": "node", "args": "--one two", "cwd": "/legacy"}},
	}
	writeMCPJob := func(mcpServers []map[string]any) {
		t.Helper()
		writeManagedJob(t, job, map[string]any{"workdir": "/work", "variables": variables, "mcpServers": mcpServers,
			"agentTools": []string{"deepseek-harness", "codex", "claude-code", "gemini-cli", "opencode", "pi"}})
	}
	writeMCPJob(servers)
	output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH=bearer-value")
	if err != nil {
		t.Fatalf("initial MCP reconciliation failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "MCP sync is unsupported for Agent tool pi") || strings.Contains(string(output), "bearer-value") || strings.Contains(string(output), "dsh-secret") {
		t.Fatalf("unsupported warning is missing or leaked a secret: %q", output)
	}
	codexPath := filepath.Join(root, "root/.codex/config.toml")
	codex, err := os.ReadFile(codexPath)
	if err != nil || strings.Count(string(codex), "[mcp_servers.alpha]") != 1 ||
		!strings.Contains(string(codex), `args = ["-y","arg with space"]`) ||
		!strings.Contains(string(codex), `cwd = "/work"`) ||
		!strings.Contains(string(codex), `[mcp_servers.legacy-args]`) ||
		!strings.Contains(string(codex), `args = ["--one","two"]`) ||
		!strings.Contains(string(codex), `env_http_headers = { "Authorization" = "AUTH_TOKEN" }`) ||
		!strings.Contains(string(codex), "[mcp_servers.user]") || strings.Contains(string(codex), "bearer-value") {
		t.Fatalf("Codex managed config = %q, %v", codex, err)
	}
	for relative, envReference := range map[string]string{
		"root/.claude.json":                   "${AUTH_TOKEN}",
		"root/.gemini/settings.json":          "$AUTH_TOKEN",
		"root/.config/opencode/opencode.json": "{env:AUTH_TOKEN}",
	} {
		config, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !strings.Contains(string(config), envReference) || !strings.Contains(string(config), `"user"`) || strings.Contains(string(config), "bearer-value") {
			t.Fatalf("%s managed config = %q, %v", relative, config, err)
		}
	}
	claude, err := os.ReadFile(filepath.Join(root, "root/.claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var claudeConfig struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(claude, &claudeConfig); err != nil {
		t.Fatalf("decode Claude managed config: %v", err)
	}
	alphaClaude := claudeConfig.MCPServers["alpha"]
	if alphaClaude.Command != "sh" || len(alphaClaude.Args) < 7 || alphaClaude.Args[0] != "-c" ||
		alphaClaude.Args[3] != "/work" || alphaClaude.Args[4] != "npx" || alphaClaude.Args[6] != "arg with space" {
		t.Fatalf("Claude cwd wrapper = %#v", alphaClaude)
	}
	opencode, err := os.ReadFile(filepath.Join(root, "root/.config/opencode/opencode.json"))
	if err != nil || !strings.Contains(string(opencode), `"cwd": "/work"`) {
		t.Fatalf("OpenCode cwd config = %q, %v", opencode, err)
	}
	dshPath := filepath.Join(root, "opt/agentbox/dsh-mcp.patch.yml")
	dsh, err := os.ReadFile(dshPath)
	if err != nil || !strings.Contains(string(dsh), `"X-Secret":"dsh-secret"`) || !strings.Contains(string(dsh), `cwd: "/work"`) {
		t.Fatalf("DSH managed patch = %q, %v", dsh, err)
	}

	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH=bearer-value")
	if err != nil {
		t.Fatalf("idempotent MCP restart failed: %v\n%s", err, output)
	}
	codex, _ = os.ReadFile(codexPath)
	if strings.Count(string(codex), "[mcp_servers.alpha]") != 1 {
		t.Fatalf("MCP restart duplicated Codex section: %q", codex)
	}

	servers = servers[1:]
	servers[0]["spec"].(map[string]any)["url"] = "https://beta-v2.invalid/mcp"
	writeMCPJob(servers)
	output, err = runManagedFixture(t, functions, root, job, secret, "opencode.json", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH=must-not-commit")
	if err == nil {
		t.Fatalf("injected MCP staging failure succeeded: %s", output)
	}
	codex, _ = os.ReadFile(codexPath)
	if !strings.Contains(string(codex), "[mcp_servers.alpha]") || strings.Contains(string(codex), "beta-v2") {
		t.Fatalf("MCP staging failure changed active config: %q", codex)
	}
	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH=bearer-value")
	if err != nil {
		t.Fatalf("MCP update/delete failed: %v\n%s", err, output)
	}
	codex, _ = os.ReadFile(codexPath)
	if strings.Contains(string(codex), "[mcp_servers.alpha]") || !strings.Contains(string(codex), "beta-v2") || !strings.Contains(string(codex), "[mcp_servers.user]") {
		t.Fatalf("MCP update/delete did not reconcile exactly: %q", codex)
	}

	writeMCPJob(nil)
	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH=bearer-value")
	if err != nil {
		t.Fatalf("MCP clear failed: %v\n%s", err, output)
	}
	codex, _ = os.ReadFile(codexPath)
	if strings.Contains(string(codex), "[mcp_servers.beta]") || strings.Contains(string(codex), "[mcp_servers.legacy]") ||
		strings.Contains(string(codex), "[mcp_servers.legacy-args]") || !strings.Contains(string(codex), "[mcp_servers.user]") {
		t.Fatalf("MCP clear removed the wrong config: %q", codex)
	}
	if _, err := os.Stat(dshPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleared DSH patch survived: %v", err)
	}
}

func TestMCPLegacyManifestMigrationReconcilesEveryPreviouslySelectedAdapter(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	root := t.TempDir()
	job := filepath.Join(t.TempDir(), "job.json")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, nil, 0600); err != nil {
		t.Fatal(err)
	}
	legacyFiles := map[string]string{
		"opt/agentbox/mcp.json":               `{"mcpServers":{"legacy":{"transport":"stdio","command":"node","args":"--old"}}}`,
		"opt/agentbox/manifest.json":          `{"agentTools":["codex","claude-code","gemini-cli","opencode"]}`,
		"root/.codex/config.toml":             "model = \"fixture\"\n[mcp_servers.user]\nurl = \"https://user.invalid\"\n[mcp_servers.legacy]\ntype = \"stdio\"\ncommand = \"node\"\nargs = [\"--old\"]\n",
		"root/.claude.json":                   `{"theme":"dark","mcpServers":{"user":{"type":"http","url":"https://user.invalid"},"legacy":{"type":"stdio","command":"node","args":["--old"]}}}`,
		"root/.gemini/settings.json":          `{"theme":"dark","mcpServers":{"user":{"httpUrl":"https://user.invalid"},"legacy":{"command":"node","args":["--old"]}}}`,
		"root/.config/opencode/opencode.json": `{"theme":"dark","mcp":{"user":{"type":"remote","url":"https://user.invalid"},"legacy":{"type":"local","command":["node","--old"],"enabled":true}}}`,
	}
	for relative, content := range legacyFiles {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJob := func(servers []map[string]any) {
		t.Helper()
		writeManagedJob(t, job, map[string]any{
			"workdir": "/workspace", "variables": []any{}, "mcpServers": servers,
			"agentTools": []string{"codex", "claude-code", "gemini-cli", "opencode"},
		})
	}
	writeJob([]map[string]any{{"id": "legacy", "name": "Legacy", "spec": map[string]any{
		"transport": "stdio", "command": "node", "arguments": []string{"--new"},
	}}})
	output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`)
	if err != nil {
		t.Fatalf("legacy MCP migration failed: %v\n%s", err, output)
	}
	for relative, tokens := range map[string][]string{
		"root/.codex/config.toml":             {"[mcp_servers.user]", "[mcp_servers.legacy]", `args = ["--new"]`},
		"root/.claude.json":                   {`"user"`, `"legacy"`, `"--new"`},
		"root/.gemini/settings.json":          {`"user"`, `"legacy"`, `"--new"`},
		"root/.config/opencode/opencode.json": {`"user"`, `"legacy"`, `"--new"`},
	} {
		config, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range tokens {
			if !strings.Contains(string(config), token) {
				t.Fatalf("%s did not migrate managed entry %q: %s", relative, token, config)
			}
		}
	}

	writeJob(nil)
	output, err = runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`)
	if err != nil {
		t.Fatalf("legacy MCP deletion failed: %v\n%s", err, output)
	}
	for _, relative := range []string{
		"root/.codex/config.toml", "root/.claude.json", "root/.gemini/settings.json", "root/.config/opencode/opencode.json",
	} {
		config, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || strings.Contains(string(config), "legacy") || !strings.Contains(string(config), "user") {
			t.Fatalf("%s deletion/preservation = (%s, %v)", relative, config, err)
		}
	}
}

func TestMCPLegacyMigrationFailsClosedWithoutAgentToolProvenance(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	root := t.TempDir()
	job := filepath.Join(t.TempDir(), "job.json")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, nil, 0600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "opt/agentbox/mcp.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"mcpServers":{"legacy":{"transport":"http","url":"https://legacy.invalid"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	writeManagedJob(t, job, map[string]any{"variables": []any{}, "mcpServers": []any{}, "agentTools": []string{"codex"}})
	output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`)
	if err == nil || !strings.Contains(string(output), "cannot safely migrate") {
		t.Fatalf("legacy MCP migration without provenance = (%q, %v)", output, err)
	}
	got, readErr := os.ReadFile(legacyPath)
	if readErr != nil || string(got) != legacy {
		t.Fatalf("failed migration changed legacy manifest: (%q, %v)", got, readErr)
	}
}

func TestCodexMCPQuotedCollisionAndSpoofedMarkers(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	for _, collision := range []bool{true, false} {
		name := "spoofed marker preservation"
		if collision {
			name = "quoted collision"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			job := filepath.Join(t.TempDir(), "job.json")
			secret := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(secret, nil, 0600); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(root, "root/.codex/config.toml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
				t.Fatal(err)
			}
			config := "# BEGIN AGENTBOX MANAGED MCP\nunrelated_setting = \"keep\"\n[mcp_servers.user]\nurl = \"https://user.invalid\"\n# END AGENTBOX MANAGED MCP\n"
			serverID := "beta"
			if collision {
				config = "[ mcp_servers . \"alpha\" ] # user-owned\nurl = \"https://user.invalid\"\n"
				serverID = "alpha"
			}
			if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			writeManagedJob(t, job, map[string]any{
				"variables": []any{}, "agentTools": []string{"codex"},
				"mcpServers": []map[string]any{{"id": serverID, "spec": map[string]any{"transport": "http", "url": "https://managed.invalid"}}},
			})
			output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`)
			got, readErr := os.ReadFile(configPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if collision {
				if err == nil || !strings.Contains(string(output), "collides with an unmanaged Codex server") || string(got) != config {
					t.Fatalf("quoted Codex collision = (%q, %v), config=%s", output, err, got)
				}
				return
			}
			if err != nil || !strings.Contains(string(got), `unrelated_setting = "keep"`) ||
				!strings.Contains(string(got), "[mcp_servers.user]") || !strings.Contains(string(got), "[mcp_servers.beta]") {
				t.Fatalf("spoofed markers deleted unrelated config: (%q, %v), config=%s", output, err, got)
			}
		})
	}
}

func TestMCPRejectsHeaderControlCharactersWithoutLeakingValue(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "install_agent_wrappers() {")
	for _, control := range []string{"\r\n", "\t", "\x7f"} {
		root := t.TempDir()
		job := filepath.Join(t.TempDir(), "job.json")
		secret := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(secret, nil, 0600); err != nil {
			t.Fatal(err)
		}
		writeManagedJob(t, job, map[string]any{
			"variables":  []map[string]any{{"id": "auth", "spec": map[string]any{"key": "AUTH_TOKEN", "mode": "value-ref", "reference": "env://HOST_AUTH"}}},
			"mcpServers": []map[string]any{{"id": "remote", "spec": map[string]any{"transport": "http", "url": "https://remote.invalid", "headers": []map[string]string{{"name": "Authorization", "valueFrom": "env://AUTH_TOKEN"}}}}},
			"agentTools": []string{"codex"},
		})
		secretValue := "must-not-leak" + control + "injected"
		output, err := runManagedFixture(t, functions, root, job, secret, "", `configure_mcp_servers fixture "$TEST_JOB"`, "HOST_AUTH="+secretValue)
		if err == nil || strings.Contains(string(output), "must-not-leak") || !strings.Contains(string(output), "AgentBox MCP configuration is invalid") {
			t.Fatalf("header control rejection = (%q, %v)", output, err)
		}
	}
}

func TestWorkerServiceEnvVariableRejectsOversizeValue(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "write_container_file_atomic() {")
	root := t.TempDir()
	job := filepath.Join(root, "job.json")
	outputPath := filepath.Join(root, "resolved.json")
	writeManagedJob(t, job, map[string]any{"variables": []map[string]any{{
		"id": "large", "spec": map[string]any{"key": "LARGE_VALUE", "mode": "value-ref", "reference": "env://HOST_LARGE"},
	}}})
	sh := managedConfigShell(t)
	mock := ""
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n"
	}
	command := exec.CommandContext(t.Context(), sh, "-s")
	large := strings.Repeat("x", 16385)
	command.Env = append(command.Environ(), "TEST_JOB="+shellPath(job), "TEST_OUTPUT="+shellPath(outputPath), "HOST_LARGE="+large)
	command.Stdin = strings.NewReader("set -eu\n" + mock + functions + `
resolve_worker_variables "$TEST_JOB" "$TEST_OUTPUT"
`)
	output, err := command.CombinedOutput()
	if err == nil || strings.Contains(string(output), large) || !strings.Contains(string(output), "LARGE_VALUE") {
		t.Fatalf("oversize env rejection = (%q, %v)", output, err)
	}
}

func TestWorkerSecretVariableRejectsUnsafeFiles(t *testing.T) {
	functions := daemonSection(t, "append_env_file() {", "write_container_file_atomic() {")
	for _, test := range []struct {
		name, reference, metadata, want string
		content                         []byte
	}{
		{name: "NUL", reference: "secret://HOST_SECRET", content: []byte{'a', 0, 'b'}, want: "contains NUL"},
		{name: "oversize", reference: "secret://HOST_SECRET", content: []byte(strings.Repeat("x", 16385)), want: "exceeds 16 KiB"},
		{name: "broad permissions", reference: "secret://HOST_SECRET", content: []byte("value"), metadata: "0:644:5", want: "permissions are too broad"},
		{name: "wrong owner", reference: "secret://HOST_SECRET", content: []byte("value"), metadata: "1000:600:5", want: "not root-owned"},
		{name: "invalid name", reference: "secret://../HOST_SECRET", content: []byte("value"), want: "reference is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			job := filepath.Join(root, "job.json")
			secret := filepath.Join(root, "secret")
			if err := os.WriteFile(secret, test.content, 0600); err != nil {
				t.Fatal(err)
			}
			writeManagedJob(t, job, map[string]any{"variables": []map[string]any{{
				"id": "secret", "spec": map[string]any{"key": "SECRET_TOKEN", "mode": "secret-ref", "reference": test.reference},
			}}})
			outputPath := filepath.Join(root, "resolved.json")
			output, err := runManagedFixture(t, functions, root, job, secret, "",
				`resolve_worker_variables "$TEST_JOB" "$TEST_ROOT/resolved.json"`, "TEST_SECRET_METADATA="+test.metadata)
			if err == nil || !strings.Contains(string(output), test.want) || strings.Contains(string(output), string(test.content)) {
				t.Fatalf("unsafe secret rejection = (%q, %v), resolved=%s", output, err, outputPath)
			}
		})
	}
}
