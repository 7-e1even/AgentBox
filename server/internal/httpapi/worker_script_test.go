package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if got := strings.Count(workerDaemon, capabilityCheck); got != 1 {
		t.Fatalf("usable Docker capability checks = %d, want 1 shared capability probe", got)
	}
	if !strings.Contains(workerDaemon, `docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable"`) {
		t.Fatal("sandbox creation does not fail fast when the Docker daemon is unavailable")
	}
}

func TestWorkerCredentialFormatsFollowProtocol(t *testing.T) {
	for _, mapping := range []string{
		`$runtimeBase + .facadePath`,
		`anthropicEndpoint: ($runtimeBase + .facadePath + "/anthropic")`,
		`openaiEndpoint: ($runtimeBase + .facadePath + "/openai/v1")`,
		`case "$PROTOCOL" in`,
		`openai-responses|openai-chat)`,
		`append_env "$ENV_FILE" OPENAI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" ANTHROPIC_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" ANTHROPIC_AUTH_TOKEN "$CLAUDE_SECRET"`,
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

func TestWorkerPreconfiguresClaudeCodeOnboarding(t *testing.T) {
	for _, expected := range []string{
		`jq '.hasCompletedOnboarding = true' "$CLAUDE_CURRENT"`,
		`test ! -s /root/.claude.json || cat /root/.claude.json`,
		`cat > /root/.claude.json`,
		`jq -s '.[0] * .[1] | .hasCompletedOnboarding = true'`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Claude Code onboarding config is missing %q", expected)
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
		`docker exec "$CONTAINER" npm install -g --force @earendil-works/pi-coding-agent@latest`,
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

func TestWorkerKeepsPiInstallerAndCredentialSyntaxCompatible(t *testing.T) {
	for _, expected := range []string{
		`if [ "$TOOL" != pi ] && docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null"`,
		`pi) INSTALL_PI=true; continue ;;`,
		`npm uninstall -g @mariozechner/pi-coding-agent`,
		`npm install -g --force @earendil-works/pi-coding-agent@latest`,
		`major === 22 && minor >= 19`,
		`apiKey: ("$AGENTBOX_KEY_" + (.id | env_id))`,
		`INSTALLER_REVISION=2`,
		`LABEL agentbox.runtime.installer-revision=$INSTALLER_REVISION`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Pi installer compatibility is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `pi) PACKAGE='@mariozechner/pi-coding-agent'`) {
		t.Fatal("Pi must not be installed from the deprecated package")
	}
}

func TestWorkerCachesLatestAgentRuntimeImage(t *testing.T) {
	for _, expected := range []string{
		`prepare_agent_image()`,
		`AGENTBOX_AGENT_IMAGE_TTL_HOURS:-24`,
		`LABEL agentbox.runtime.cache=true`,
		`LABEL agentbox.runtime.refreshed=$NOW`,
		`cat > /opt/agentbox/agent-versions.json`,
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

func TestWorkerWritesSmallRuntimeFilesThroughStdin(t *testing.T) {
	for _, expected := range []string{
		`docker exec "$CONTAINER" rm -rf /opt/agentbox/agent-versions.json`,
		`docker exec -i "$CONTAINER" sh -c 'cat > /opt/agentbox/agent-versions.json' < "$VERSION_FILE"`,
		`docker exec "$TARGET" rm -rf /opt/agentbox/manifest.json`,
		`docker exec -i "$TARGET" sh -c 'cat > /opt/agentbox/manifest.json' < "$MANIFEST"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("runtime file provisioning is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `$CONTAINER:/opt/agentbox/agent-versions.json`) ||
		strings.Contains(workerDaemon, `$TARGET:/opt/agentbox/manifest.json`) {
		t.Fatal("small runtime files must not use BoxLite's archive-oriented copy path")
	}
}

func TestWorkerCreatesSandboxWithExplicitRootUser(t *testing.T) {
	if !strings.Contains(workerDaemon, `--restart unless-stopped --user 0:0 --workdir "$WORKDIR"`) {
		t.Fatal("sandbox creation does not explicitly select the container root user")
	}
}

func TestWorkerRestartReappliesSandboxConfiguration(t *testing.T) {
	for _, expected := range []string{
		`restart_sandbox()`,
		`restart-sandbox) OPERATION=restart_sandbox`,
		`configure_credentials "$TARGET" "$JOB_FILE"`,
		`install_agent_wrappers "$TARGET" "$JOB_FILE"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("sandbox restart support is missing %q", expected)
		}
	}
}

func TestWorkerHasNoDedicatedAgentAccountLogin(t *testing.T) {
	for _, removed := range []string{
		`login_agent()`,
		`login-agent)`,
		`codex login --device-auth`,
	} {
		if strings.Contains(workerDaemon, removed) {
			t.Fatalf("worker still contains removed account login flow %q", removed)
		}
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
		`KIMI_ENDPOINT=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.chatEndpoint')`,
		`"anthropic-messages"`,
		`"openai-responses"`,
		`"google-generative-ai"`,
		`apiKey: ("$AGENTBOX_KEY_" + (.id | env_id))`,
		`cat > /root/.pi/agent/models.json`,
		`cat > /root/.pi/agent/settings.json`,
		`cat > /root/.reasonix/config.toml`,
		`cp /opt/agentbox/secrets/agentbox.env /root/.reasonix/.env`,
		`"bash = \"off\""`,
		`elif . == "openai-chat" then "openai"`,
		`if . == "openai-responses" then "responses"`,
		`("api_key_env = " + ("AGENTBOX_KEY_\(.id | env_id)" | @json))`,
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

func TestWorkerInjectsSandboxEnvironmentVariables(t *testing.T) {
	for _, expected := range []string{
		`.job.payload.environmentVariables[]?`,
		`append_env "$ENV_FILE" "$NAME" "$VALUE"`,
		`cat > /etc/profile.d/agentbox-env.sh`,
		`. /opt/agentbox/secrets/agentbox.env`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("sandbox environment injection is missing %q", expected)
		}
	}
}

func TestWorkerInstallerIncludesInteractiveSessionDaemon(t *testing.T) {
	for _, expected := range []string{
		`/api/worker/agentbox-worker?arch=$ARCH`,
		`install -m 0755 "$worker_tmp" /usr/local/bin/agentbox-worker`,
		`install_host_dependencies`,
		`migrate_existing_config`,
		`if ! go mod tidy; then`,
		`GOPROXY=https://goproxy.cn,direct go mod tidy`,
		`printf '%s\n%s\n%s\n' "$SERVER_URL" "$SERVER_ID" "$CREDENTIAL" > "$CONFIG"`,
		`systemctl restart agentbox-worker.service`,
	} {
		if !strings.Contains(workerInstall, expected) {
			t.Fatalf("interactive worker installer is missing %q", expected)
		}
	}
	if !strings.Contains(workerDaemon, `/usr/local/bin/agentbox-worker session "$CONFIG" &`) {
		t.Fatal("worker service does not start the interactive session daemon")
	}
	if !strings.Contains(workerDaemon, `CAPS="$CAPS\"interactive-session\""`) {
		t.Fatal("worker heartbeat does not advertise interactive session support")
	}
	if strings.Contains(workerDaemon, "workspace-exec") {
		t.Fatal("legacy queued terminal execution must not remain in the worker")
	}
}

func TestWorkerSupportsVersionedAtomicSelfUpdate(t *testing.T) {
	for _, expected := range []string{
		`--arg workerVersion "$AGENTBOX_WORKER_VERSION"`,
		`LEGACY_HEARTBEAT=$(printf '%s' "$HEARTBEAT" | jq 'del(.workerVersion)')`,
		`update-worker)`,
		`/api/worker/agentbox-worker?arch=$ARCH&version=$TARGET_VERSION`,
		`refresh_microsandbox_driver`,
		`/api/worker/agentbox-microsandbox-driver.go`,
		`export GOPATH="$STATE_DIR/go"`,
		`export GOMODCACHE="$STATE_DIR/go-mod-cache"`,
		`export GOCACHE="$STATE_DIR/go-build-cache"`,
		`DOWNLOADED_VERSION=$("$WORKER_TMP" version`,
		`/usr/local/lib/agentbox/agentbox-worker.previous`,
		`mv -f "$NEXT" /usr/local/bin/agentbox-worker`,
		`systemd-run --quiet --unit="agentbox-worker-update-$JOB_ID" --on-active=20s`,
		`systemd-run --quiet --unit="agentbox-worker-restart-$JOB_ID" --on-active=1s`,
		`finalize_worker_update`,
		`trap cleanup_worker EXIT`,
		`trap 'exit 0' INT TERM`,
		`stop_boxlite_server`,
		`Worker 更新已回滚`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker self-update is missing %q", expected)
		}
	}
}

func TestWorkerInstallerIncludesPinnedMicroVMRuntimeSDKs(t *testing.T) {
	for _, expected := range []string{
		`BOXLITE_VERSION=0.9.7`,
		`boxlite-cli-v${BOXLITE_VERSION}-${BOXLITE_ARCH}-unknown-linux-gnu.tar.gz`,
		`sha256sum -c`,
		`/usr/local/bin/boxlite`,
		`/api/worker/agentbox-microsandbox-driver.go`,
		`github.com/superradcompany/microsandbox/sdk/go v0.6.8`,
		`GOPROXY=https://goproxy.cn,direct go mod tidy`,
		`CGO_ENABLED=1 go build -tags agentbox_driver`,
		`/usr/local/bin/agentbox-microsandbox-driver`,
		`Runtime capabilities will be published after self-test`,
	} {
		if !strings.Contains(workerInstall, expected) {
			t.Fatalf("runtime SDK installer is missing %q", expected)
		}
	}
	for _, expected := range []string{
		`runtime_probe boxlite`,
		`CAPS="$CAPS\"boxlite\""`,
		`boxlite_call`,
		`prepare_boxlite_config`,
		`boxlite_server_ready`,
		`Handle invalidated after stop()`,
		`boxlite_start "$TARGET"`,
		`--host 127.0.0.1 --port 48100`,
		`/usr/local/bin/boxlite --url "$BOXLITE_URL"`,
		`boxlite_cli create --detach`,
		`WORKER_OCI_IMAGE_DIR=$STATE_DIR/oci-images`,
		`command docker image save -o "$ARCHIVE_PATH" "$IMAGE"`,
		`agentbox-worker image-to-oci --archive "$ARCHIVE_PATH"`,
		`boxlite_create_with_rootfs`,
		`rootfs_path: $rootfsPath`,
		`source: "worker-oci"`,
		`boxlite_local_cli pull "$IMAGE"`,
		`boxlite_local_cli images --all --format json`,
		`BOXLITE_IMAGES_FILE=$STATE_DIR/boxlite-images.json`,
		`refresh_boxlite_images`,
		`boxlite_cli exec`,
		`boxlite_local_cli cp /usr/local/bin/agentbox-worker`,
		`guest-fs exists /opt/agentbox/agentbox-guest`,
		`guest-fs mkdir "$WORKDIR"`,
		`runtime_probe microsandbox`,
		`CAPS="$CAPS\"microsandbox\""`,
		`runtime_call microsandbox images`,
		`microsandbox_prepare_image`,
		`tar -C "$OCI_PATH" -cf "$OCI_ARCHIVE" oci-layout index.json blobs`,
		`prepare-image "$IMAGE" --archive "$OCI_ARCHIVE"`,
		`microsandboxImages:$microsandboxImages`,
		`runtime_call "$DRIVER" create`,
		`AgentBox Microsandbox Go driver is unavailable`,
		`/usr/local/bin/agentbox-microsandbox-driver "$@"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("runtime SDK worker integration is missing %q", expected)
		}
	}
	for _, removed := range []string{"pip install", "python3 -m venv", "/api/worker/agentbox-runtime-driver"} {
		if strings.Contains(workerInstall, removed) {
			t.Fatalf("Worker installer still depends on Python runtime path %q", removed)
		}
	}
}

func TestWorkerBoxLitePrefersLocalDockerBeforeRegistry(t *testing.T) {
	prepareStart := strings.Index(workerDaemon, "boxlite_prepare_image()")
	prepareEnd := strings.Index(workerDaemon[prepareStart:], "install_boxlite_guest()")
	if prepareStart < 0 || prepareEnd < 0 {
		t.Fatal("BoxLite image preparation function was not found")
	}
	prepareBody := workerDaemon[prepareStart : prepareStart+prepareEnd]
	localIndex := strings.Index(prepareBody, `worker_local_oci_image "$IMAGE"`)
	registryIndex := strings.Index(prepareBody, `boxlite_local_cli pull "$IMAGE"`)
	if localIndex < 0 || registryIndex < 0 || localIndex >= registryIndex {
		t.Fatalf("BoxLite image preparation must check Worker-local Docker before Registry pull")
	}

	for _, expected := range []string{
		`POST "$BOXLITE_URL/v1/boxes"`,
		`rootfs_path: $rootfsPath`,
		`disk_size_gb: $diskSize`,
		`DISK_SIZE=${AGENTBOX_BOXLITE_DISK_SIZE_GB:-20}`,
		`--disk-size "$DISK_SIZE"`,
		`unique_by(.source, .id, .reference)`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite local-image bridge is missing %q", expected)
		}
	}
}

func TestWorkerBoxLiteServerLifecycleIsOwnedByMainLoop(t *testing.T) {
	heartbeatStart := strings.Index(workerDaemon, "heartbeat_loop()")
	heartbeatEnd := strings.Index(workerDaemon[heartbeatStart:], "run_worker()")
	if heartbeatStart < 0 || heartbeatEnd < 0 {
		t.Fatal("Worker heartbeat function was not found")
	}
	heartbeatBody := workerDaemon[heartbeatStart : heartbeatStart+heartbeatEnd]
	if strings.Contains(heartbeatBody, "ensure_boxlite_server") {
		t.Fatal("background heartbeat must not race the main loop for the BoxLite runtime lock")
	}

	runStart := strings.Index(workerDaemon, "run_worker()")
	if runStart < 0 {
		t.Fatal("Worker main loop was not found")
	}
	runBody := workerDaemon[runStart:]
	ensureIndex := strings.LastIndex(runBody, `ensure_boxlite_server || true`)
	claimIndex := strings.LastIndex(runBody, `/jobs/claim`)
	if ensureIndex < 0 || claimIndex < 0 || ensureIndex >= claimIndex {
		t.Fatal("Worker main loop must restore the BoxLite server before claiming the next job")
	}
	for _, expected := range []string{
		`BOXLITE_SERVER_PID_FILE=$STATE_DIR/boxlite-serve.pid`,
		`BOXLITE_SERVER_PID=$(tr -d '\r\n' < "$BOXLITE_SERVER_PID_FILE")`,
		`printf '%s\n' "$BOXLITE_SERVER_PID" > "$BOXLITE_SERVER_PID_FILE"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite cross-job PID management is missing %q", expected)
		}
	}
}

func TestWorkerBoxLiteDeleteIsIdempotent(t *testing.T) {
	for _, expected := range []string{
		`HTTP_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' "$BOXLITE_URL/v1/boxes/$1")`,
		`404) return 0 ;;`,
		`boxlite_cli rm --force "$1"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite idempotent delete is missing %q", expected)
		}
	}
}

func TestWorkerMicrosandboxImportsSharedOCIImageBeforeRegistryFallback(t *testing.T) {
	prepareStart := strings.Index(workerDaemon, "microsandbox_prepare_image()")
	prepareEnd := strings.Index(workerDaemon[prepareStart:], "boxlite_create_with_rootfs()")
	if prepareStart < 0 || prepareEnd < 0 {
		t.Fatal("Microsandbox image preparation function was not found")
	}
	prepareBody := workerDaemon[prepareStart : prepareStart+prepareEnd]
	localIndex := strings.Index(prepareBody, `worker_local_oci_image "$IMAGE"`)
	archiveIndex := strings.Index(prepareBody, `prepare-image "$IMAGE" --archive "$OCI_ARCHIVE"`)
	registryIndex := strings.LastIndex(prepareBody, `runtime_call microsandbox prepare-image "$IMAGE"`)
	if localIndex < 0 || archiveIndex < localIndex || registryIndex < archiveIndex {
		t.Fatalf("Microsandbox must import the shared Worker OCI image before Registry fallback")
	}
}

func TestWorkerShellScriptsHaveValidSyntax(t *testing.T) {
	sh, err := exec.LookPath("sh")
	useWSL := false
	if err != nil && runtime.GOOS == "windows" {
		wsl, wslErr := exec.LookPath("wsl")
		if wslErr == nil && exec.Command(wsl, "sh", "-c", "true").Run() == nil {
			sh = wsl
			err = nil
			useWSL = true
		}
	}
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	for name, script := range map[string]string{
		"install.sh":      workerInstall,
		"agentbox-worker": workerDaemon,
	} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(sh, "-n", path)
		if useWSL {
			linuxPath, err := exec.Command(sh, "wslpath", "-a", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			command = exec.Command(sh, "sh", "-n", strings.TrimSpace(string(linuxPath)))
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s syntax: %v\n%s", name, err, output)
		}
	}
}
