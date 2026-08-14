package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerSetupChecksDependenciesBeforeRegistration(t *testing.T) {
	for _, check := range []string{
		`command -v curl >/dev/null || { echo "curl is required"`,
		`command -v jq >/dev/null || { echo "jq is required"`,
		`command -v systemctl >/dev/null || { echo "systemd is required"`,
	} {
		if !strings.Contains(workerDaemon, check) {
			t.Fatalf("worker setup is missing dependency check %q", check)
		}
	}
}

func TestWorkerOnlyAdvertisesUsableDocker(t *testing.T) {
	const capabilityCheck = `command -v docker >/dev/null && docker info >/dev/null 2>&1 && CAPS='"docker"'`
	if got := strings.Count(workerDaemon, capabilityCheck); got != 2 {
		t.Fatalf("usable Docker capability checks = %d, want 2", got)
	}
	if !strings.Contains(workerDaemon, `docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable"`) {
		t.Fatal("sandbox creation does not fail fast when the Docker daemon is unavailable")
	}
}

func TestWorkerCredentialFormatsFollowProtocol(t *testing.T) {
	for _, mapping := range []string{
		`case "$PROTOCOL" in`,
		`openai-responses|openai-chat)`,
		`append_env "$ENV_FILE" OPENAI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" ANTHROPIC_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" GEMINI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" "AGENTBOX_KEY_$ENV_ID" "$SECRET"`,
	} {
		if !strings.Contains(workerDaemon, mapping) {
			t.Fatalf("worker credential mapping is missing %q", mapping)
		}
	}
}

func TestWorkerAgentConfigsMatchSupportedProtocols(t *testing.T) {
	for _, config := range []string{
		`CODEX_ENDPOINT=https://api.openai.com/v1`,
		`env_key = "AGENTBOX_KEY_%s"`,
		`wire_api = "responses"`,
		`any(.protocol == "gemini")`,
		`elif $credential.protocol == "openai-responses" then "@ai-sdk/openai"`,
		`else "@ai-sdk/openai-compatible" end`,
		`if $credential.protocol == "anthropic" and . != "" and (endswith("/v1") | not)`,
	} {
		if !strings.Contains(workerDaemon, config) {
			t.Fatalf("worker Agent config is missing %q", config)
		}
	}
}

func TestWorkerInstallsExtendedAgentTools(t *testing.T) {
	for _, expected := range []string{
		`codebuddy) PACKAGE='@tencent-ai/codebuddy-code'`,
		`grok) PACKAGE='@xai-official/grok'`,
		`kimi) PACKAGE='@moonshot-ai/kimi-code'`,
		`omp) PACKAGE='@oh-my-pi/pi-coding-agent'`,
		`openclaw) PACKAGE='openclaw'`,
		`pi) PACKAGE='@mariozechner/pi-coding-agent'`,
		`copilot-cli) PACKAGE='@github/copilot'`,
		`qoder-cli) PACKAGE='@qoder-ai/qodercli'`,
		`qoder-cn) PACKAGE='@qodercn-ai/qoderclicn'`,
		`qwen-code) PACKAGE='@qwen-code/qwen-code'`,
		`reasonix) PACKAGE='reasonix'`,
		`antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$PLATFORM.json`,
		`sha512sum -c -`,
		`https://cursor.com/install`,
		`timeout 3600 /root/.local/bin/uv tool install --python 3.12 qwenpaw`,
		`DevEco Code does not support Linux sandboxes`,
		`TRAE CLI has no verified unattended Linux installer`,
		`antigravity) printf '%s' agy`,
		`cursor) printf '%s' cursor-agent`,
		`omp) printf '%s' omp`,
		`/root/.pi/agent/skills/$ID`,
		`/root/.omp/agent/skills/$ID`,
		`/root/.copilot/skills/$ID`,
		`/root/.qwen/skills/$ID`,
		`/root/.qwenpaw/skill_pool/$ID`,
		`/root/.agents/skills/$ID`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("extended Agent support is missing %q", expected)
		}
	}
}

func TestWorkerCachesLatestAgentRuntimeImage(t *testing.T) {
	for _, expected := range []string{
		`prepare_agent_image()`,
		`AGENTBOX_AGENT_IMAGE_TTL_HOURS:-24`,
		`LABEL agentbox.runtime.cache=true`,
		`LABEL agentbox.runtime.refreshed=$NOW`,
		`$CONTAINER:/opt/agentbox/agent-versions.json`,
		`Agent tool $TOOL was installed but command $COMMAND is unavailable`,
		`RUNTIME_IMAGE=$(prepare_agent_image "$IMAGE" "$JOB_FILE")`,
		`set -- "$@" "$RUNTIME_IMAGE"`,
		`Agent runtime refresh failed; using the last working cached image`,
		`BUILD_CONTAINER="agentbox-runtime-build-$CACHE_KEY"`,
		`docker stop --time 10 "$BUILD_CONTAINER"`,
		`command -v $COMMAND >/dev/null`,
		`curl --connect-timeout 15 --max-time 300`,
		`best_cached_agent_base()`,
		`BUILD_BASE_IMAGE=$(best_cached_agent_base "$BASE_IMAGE" "$TOOLS" "$NOW" "$TTL_SECONDS")`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Agent runtime cache is missing %q", expected)
		}
	}
	if !strings.Contains(workerDaemon, `select((.Repository | startswith("agentbox/runtime-")) | not)`) {
		t.Fatal("worker inventory must hide internal Agent runtime cache images")
	}
	if got := strings.Count(workerDaemon, `install_agent_tools "$BUILD_CONTAINER" "$JOB_FILE"`); got != 1 {
		t.Fatalf("cached image install calls = %d, want 1", got)
	}
}

func TestWorkerCreatesSandboxWithExplicitRootUser(t *testing.T) {
	if !strings.Contains(workerDaemon, `--restart unless-stopped --user 0:0 --workdir "$WORKDIR"`) {
		t.Fatal("sandbox creation does not explicitly select the container root user")
	}
}

func TestWorkerConfiguresExtendedAgentCredentials(t *testing.T) {
	for _, expected := range []string{
		`append_env "$ENV_FILE" COPILOT_PROVIDER_API_KEY "$COPILOT_SECRET"`,
		`append_env "$ENV_FILE" COPILOT_PROVIDER_BASE_URL "$COPILOT_ENDPOINT"`,
		`append_env "$ENV_FILE" KIMI_MODEL_NAME "$KIMI_MODEL"`,
		`append_env "$ENV_FILE" KIMI_MODEL_API_KEY "$KIMI_SECRET"`,
		`append_env "$ENV_FILE" KIMI_MODEL_PROVIDER_TYPE "$KIMI_PROVIDER_TYPE"`,
		`append_env "$ENV_FILE" KIMI_MODEL_BASE_URL "$KIMI_ENDPOINT"`,
		`"anthropic-messages"`,
		`"openai-responses"`,
		`"google-generative-ai"`,
		`apiKey: ("AGENTBOX_KEY_" + (.id | env_id))`,
		`cat > /root/.pi/agent/models.json`,
		`cat > /root/.pi/agent/settings.json`,
		`cat > /root/.qwen/settings.json`,
		`append_env "$ENV_FILE" CURSOR_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" XAI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" QODERCN_PERSONAL_ACCESS_TOKEN "$SECRET"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("extended Agent credential config is missing %q", expected)
		}
	}
}

func TestWorkerInstallerIncludesInteractiveSessionDaemon(t *testing.T) {
	for _, expected := range []string{
		`/api/worker/agentbox-session-worker`,
		`/usr/local/lib/agentbox/session_worker.py`,
		`python3 is required for interactive sessions`,
		`systemctl restart agentbox-worker.service`,
	} {
		if !strings.Contains(workerInstall, expected) {
			t.Fatalf("interactive worker installer is missing %q", expected)
		}
	}
	if !strings.Contains(workerDaemon, `python3 /usr/local/lib/agentbox/session_worker.py "$CONFIG" &`) {
		t.Fatal("worker service does not start the interactive session daemon")
	}
	if !strings.Contains(workerDaemon, `CAPS="$CAPS\"interactive-session\""`) {
		t.Fatal("worker heartbeat does not advertise interactive session support")
	}
	if strings.Contains(workerDaemon, "workspace-exec") {
		t.Fatal("legacy queued terminal execution must not remain in the worker")
	}
}

func TestWorkerSessionDaemonHasValidPythonSyntax(t *testing.T) {
	python := ""
	for _, candidate := range []string{"python3", "python"} {
		if path, err := exec.LookPath(candidate); err == nil && exec.Command(path, "--version").Run() == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Skip("Python is not available on this test host")
	}
	path := filepath.Join(t.TempDir(), "session_worker.py")
	if err := os.WriteFile(path, []byte(workerSessionDaemon), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(python, "-m", "py_compile", path).CombinedOutput(); err != nil {
		t.Fatalf("session worker syntax: %v\n%s", err, output)
	}
}
