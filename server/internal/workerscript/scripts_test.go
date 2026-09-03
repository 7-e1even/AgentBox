package workerscript

import (
	"crypto/sha256"
	"fmt"
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
	const capabilityCheck = `command -v docker >/dev/null && timeout 10 docker info >/dev/null 2>&1 && CAPS='"docker"'`
	if got := strings.Count(workerDaemon, capabilityCheck); got != 1 {
		t.Fatalf("usable Docker capability checks = %d, want 1 shared capability probe", got)
	}
	if !strings.Contains(workerDaemon, `timeout 10 docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable"`) {
		t.Fatal("sandbox creation does not fail fast when the Docker daemon is unavailable")
	}
}

func TestWorkerCredentialFormatsFollowProtocol(t *testing.T) {
	for _, mapping := range []string{
		`RUNTIME_BASE=$(sandbox_runtime_base_url "$DRIVER")`,
		`endpoint: ($runtimeBase + .facadePath`,
		`anthropicEndpoint: ($runtimeBase + .facadePath + "/anthropic")`,
		`openaiEndpoint: ($runtimeBase + .facadePath + "/openai/v1")`,
		`case "$PROTOCOL" in`,
		`openai-responses|openai-chat)`,
		`append_env "$ENV_FILE" OPENAI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" ANTHROPIC_API_KEY "$SECRET"`,
		`replace_env "$ENV_FILE" ANTHROPIC_AUTH_TOKEN "$CLAUDE_SECRET"`,
		`append_env "$ENV_FILE" GEMINI_API_KEY "$SECRET"`,
		`append_env "$ENV_FILE" "AGENTBOX_KEY_$ENV_ID" "$SECRET"`,
	} {
		if !strings.Contains(workerDaemon, mapping) {
			t.Fatalf("worker credential mapping is missing %q", mapping)
		}
	}
}

func TestWorkerRestrictedNetworkAllowsItsReachableControlPlane(t *testing.T) {
	for _, expected := range []string{
		`server_url_host()`,
		`sandbox_runtime_base_url()`,
		`sandbox_control_plane_host()`,
		`CONTROL_PLANE_HOST=$(sandbox_control_plane_host "$DRIVER")`,
		`for ALLOWED_HOST in $CONTROL_PLANE_HOST $(jq -r`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker control-plane networking is missing %q", expected)
		}
	}
}

func TestWorkerUsesOnlyBearerTokenForClaudeCodeGateway(t *testing.T) {
	start := strings.Index(workerDaemon, `if jq -e '.job.payload.agentTools | index("claude-code")'`)
	if start < 0 {
		t.Fatal("Claude Code credential block was not found")
	}
	end := strings.Index(workerDaemon[start:], `docker exec "$CONTAINER" mkdir -p /opt/agentbox/secrets`)
	if end < 0 {
		t.Fatal("Claude Code credential block end was not found")
	}
	body := workerDaemon[start : start+end]
	for _, expected := range []string{
		`remove_env "$ENV_FILE" ANTHROPIC_API_KEY`,
		`replace_env "$ENV_FILE" ANTHROPIC_AUTH_TOKEN "$CLAUDE_SECRET"`,
		`replace_env "$ENV_FILE" ANTHROPIC_BASE_URL "$CLAUDE_ENDPOINT"`,
		`replace_env "$ENV_FILE" ANTHROPIC_MODEL "$CLAUDE_MODEL"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Claude Code gateway configuration is missing %q", expected)
		}
	}
	if strings.Contains(body, `append_env "$ENV_FILE" ANTHROPIC_API_KEY "$CLAUDE_SECRET"`) {
		t.Fatal("Claude Code must not set ANTHROPIC_API_KEY together with ANTHROPIC_AUTH_TOKEN")
	}
}

func TestWorkerConfiguresClaudeCodeForK3_256K(t *testing.T) {
	start := strings.Index(workerDaemon, `if jq -e '.job.payload.agentTools | index("claude-code")'`)
	if start < 0 {
		t.Fatal("Claude Code credential block was not found")
	}
	end := strings.Index(workerDaemon[start:], `docker exec "$CONTAINER" mkdir -p /opt/agentbox/secrets`)
	if end < 0 {
		t.Fatal("Claude Code credential block end was not found")
	}
	body := workerDaemon[start : start+end]
	for _, expected := range []string{
		`if [ "$CLAUDE_MODEL" = k3-256k ]`,
		`ANTHROPIC_DEFAULT_FABLE_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL`,
		`ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL`,
		`CLAUDE_CODE_SUBAGENT_MODEL`,
		`replace_env "$ENV_FILE" "$KEY" "$CLAUDE_MODEL"`,
		`CLAUDE_CODE_MAX_CONTEXT_TOKENS CLAUDE_CODE_AUTO_COMPACT_WINDOW`,
		`grep -q "^$KEY=" "$ENV_FILE" || append_env "$ENV_FILE" "$KEY" 262144`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Claude Code K3-256K configuration is missing %q", expected)
		}
	}
	if strings.Contains(body, `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT`) {
		t.Fatal("Claude Code K3-256K must keep proactive window enforcement enabled")
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
		`deepseek-harness) PACKAGE='@deepseek-ai/dsh@0.1.0-rc.7'`,
		`set -- "$@" "$TOOL" "$PACKAGE_SPEC"`,
		`grok) continue`,
		`https://x.ai/cli/install.sh`,
		`GROK_CHANNEL=stable GROK_BIN_DIR=/usr/local/bin bash "$INSTALLER"`,
		`test -x /usr/local/bin/grok`,
		`kimi) PACKAGE='@moonshot-ai/kimi-code'`,
		`omp) PACKAGE='@oh-my-pi/pi-coding-agent'`,
		`openclaw) PACKAGE='openclaw'`,
		`agent_tool_exec_logged "$CONTAINER" pi npm install -g --force @earendil-works/pi-coding-agent@latest`,
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
		`deepseek-harness) printf '%s' dsh`,
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
	if strings.Contains(workerDaemon, `grok) PACKAGE='@xai-official/grok'`) {
		t.Fatal("Grok Build installation must keep using the official released binary installer")
	}
}

func TestWorkerInstallsOpenCodeWithoutGuestNPMForBoxLite(t *testing.T) {
	for _, expected := range []string{
		`INSTALL_OPENCODE=false`,
		`INSTALL_OPENCODE=true`,
		`PACKAGE='opencode-ai'`,
		`install_boxlite_opencode "$CONTAINER"`,
		`PLATFORM_PACKAGE=opencode-linux-x64`,
		`PLATFORM_PACKAGE=opencode-linux-arm64`,
		`"https://registry.npmjs.org/$PLATFORM_PACKAGE/latest"`,
		`curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL`,
		`curl --connect-timeout 15 --max-time 300 --retry 3 -fsSL`,
		`EXPECTED_SHA512=$(printf '%s' "${INTEGRITY#sha512-}" | base64 -d`,
		`ACTUAL_SHA512=$(sha512sum "$ARCHIVE"`,
		`tar -xzf "$ARCHIVE" -C "$TMP_DIR" package/bin/opencode`,
		`copy_boxlite_agent_payload "$CONTAINER" "$TMP_DIR/package/bin/opencode"`,
		`/opt/agentbox/opencode.upload`,
		`boxlite_local_cli start "$BOXLITE_COPY_TARGET"`,
		`boxlite_local_cli cp "$BOXLITE_COPY_SOURCE"`,
		`UPLOAD=/opt/agentbox/opencode.upload`,
		`agent_tool_exec "$CONTAINER" sh -lc`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite OpenCode binary installer is missing %q", expected)
		}
	}
}

func TestWorkerInstallsBoxLiteCodexFromOfficialNPMPackage(t *testing.T) {
	start := strings.Index(workerDaemon, "install_boxlite_codex()")
	end := strings.Index(workerDaemon[start:], "\ninstall_agent_tools()")
	if start < 0 || end < 0 {
		t.Fatal("BoxLite Codex installer boundary was not found")
	}
	body := workerDaemon[start : start+end]
	for _, expected := range []string{
		`'https://registry.npmjs.org/@openai%2fcodex/latest'`,
		`PLATFORM_VERSION=${PLATFORM_SPEC#npm:@openai/codex@}`,
		`EXPECTED_SHA512=$(printf '%s' "${INTEGRITY#sha512-}" | base64 -d`,
		`PART_COUNT=8`,
		`curl --connect-timeout 15 --max-time 900 --retry 3 -fsSL`,
		`archive_is_valid`,
		`tar -xzf "$ARCHIVE" -C "$HOST_PACKAGE_TMP" --strip-components=1`,
		`"$BINARY_MEMBER"`,
		`publish_boxlite_codex_package "$CONTAINER" "$HOST_PACKAGE"`,
		`boxlite_local_cli cp`,
		`$BOXLITE_CODEX_HOST_PACKAGE/$BOXLITE_CODEX_BINARY_REL`,
		`setsid -f sh -lc '\''sync'\''`,
		`checkpoint_boxlite_agent_update "$BOXLITE_CODEX_TARGET"`,
		`$BOXLITE_URL/v1/boxes/$BOXLITE_CHECKPOINT_TARGET/snapshots`,
		`[ "$BOXLITE_CHECKPOINT_STATUS" != 201 ]`,
		`boxlite_cli stop "$BOXLITE_CHECKPOINT_TARGET"`,
		`[ "$BOXLITE_CHECKPOINT_BOX_STATUS" = stopped ]`,
		`libkrun keeps an open fd to the live qcow2 inode`,
		`boxlite_cli start "$BOXLITE_CHECKPOINT_TARGET"`,
		`Codex publish was interrupted; restoring the BoxLite control service and retrying from the Worker cache`,
		`[ "$#" -gt 0 ] || [ "$INSTALL_PI" = true ]`,
		`codex) printf '%s' npm`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite Codex npm installer is missing %q", expected)
		}
	}
	if strings.Contains(body, "api.github.com/repos/openai/codex") || strings.Contains(body, "npm install -g") {
		t.Fatal("BoxLite Codex updates must use the reachable staged npm package path")
	}
}

func TestWorkerReusesValidatedBoxLiteCodexVersionCache(t *testing.T) {
	start := strings.Index(workerDaemon, "install_boxlite_codex()")
	end := strings.Index(workerDaemon[start:], "\ninstall_agent_tools()")
	if start < 0 || end < 0 {
		t.Fatal("BoxLite Codex installer boundary was not found")
	}
	body := workerDaemon[start : start+end]
	for _, expected := range []string{
		`FINAL="/opt/agentbox/tools/codex/$1/$2"`,
		`[ -x "$FINAL" ]`,
		`"$FINAL" --version | grep -Fq "$1"`,
		`report_agent_tool_progress codex cached "Codex $LATEST 已在沙箱缓存中"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("BoxLite Codex version cache is missing %q", expected)
		}
	}
}

func TestWorkerPublishesBoxLiteCodexAtomicallyAfterValidation(t *testing.T) {
	start := strings.Index(workerDaemon, "install_boxlite_codex()")
	end := strings.Index(workerDaemon[start:], "\ninstall_agent_tools()")
	if start < 0 || end < 0 {
		t.Fatal("BoxLite Codex installer boundary was not found")
	}
	binaryValidation := strings.Index(workerDaemon, `"$STAGE_BINARY" --version | grep -Fq "$LATEST"`)
	binaryPublish := strings.Index(workerDaemon, `mv "$STAGE" "$FINAL"`)
	linkValidation := strings.Index(workerDaemon[binaryPublish:], `"$LINK_TMP" --version | grep -Fq "$LATEST"`)
	linkPublish := strings.Index(workerDaemon[binaryPublish:], `mv -f "$LINK_TMP" /usr/local/bin/codex`)
	if binaryValidation < 0 || binaryPublish < 0 || binaryValidation >= binaryPublish ||
		linkValidation < 0 || linkPublish < 0 || linkValidation >= linkPublish {
		t.Fatal("Codex must be runnable before the live command is replaced atomically")
	}
}

func TestWorkerSkipsReadyAgentToolPrerequisites(t *testing.T) {
	for _, expected := range []string{
		`for command in curl git jq unzip python3; do command -v "$command" >/dev/null || exit 1; done`,
		`Agent 工具依赖已就绪`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Agent tool prerequisite cache is missing %q", expected)
		}
	}
}

func TestWorkerRecoversBoxLitePortalDuringAgentToolInstall(t *testing.T) {
	for _, expected := range []string{
		`recover_boxlite_agent_tool_install()`,
		`BoxLite portal disconnected while installing Agent tools; restarting the sandbox and retrying once`,
		`boxlite_cli restart "$CONTAINER" >/dev/null 2>"$RESTART_LOG"`,
		`grep -Fq 'Handle invalidated after stop()' "$RESTART_LOG"`,
		`stop_boxlite_server`,
		`ensure_boxlite_server || return 1`,
		`boxlite_cli start "$CONTAINER"`,
		`Agent tool prerequisite installation failed`,
		`Agent tool package installation failed`,
		`agent_tool_exec_stdin "$CONTAINER" "$VERSION_FILE"`,
		`could not install or verify the selected Agent tools in the $DRIVER sandbox`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite Agent tool recovery is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `is not compatible with the selected Agent tools`) {
		t.Fatal("Agent tool failures must not be reported as image incompatibility without evidence")
	}
}

func TestWorkerKeepsLargeBoxLiteAgentToolOutputOffAttachStream(t *testing.T) {
	for _, expected := range []string{
		`agent_tool_exec_logged()`,
		`if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" != boxlite ]; then`,
		`AGENT_EXEC_JOB_DIR=/opt/agentbox/jobs`,
		`AGENT_EXEC_LOG_FILE="$AGENT_EXEC_JOB_DIR/$LABEL-install.log"`,
		`AGENT_EXEC_STATUS_FILE="$AGENT_EXEC_LOG_FILE.status"`,
		`INSTALL_ATTEMPT=1`,
		`while [ "$INSTALL_ATTEMPT" -le 2 ]; do`,
		`boxlite_cli exec -d -u 0:0 "$CONTAINER" -- sh -lc`,
		`printf "%s\n" "$CODE" >"$STATUS_FILE.tmp"`,
		`mv -f "$STATUS_FILE.tmp" "$STATUS_FILE"`,
		`DEADLINE=$(($(date +%s) + ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800}))`,
		`STATUS=$(docker exec "$CONTAINER" sh -lc 'test -f "$1" && cat "$1" || true'`,
		`PORTAL_FAILURES=0`,
		`BoxLite portal remained unavailable while polling Agent tool installation; waiting for the detached task before recovery`,
		`sleep 15`,
		`if ! recover_boxlite_agent_tool_install "$CONTAINER" ||`,
		`sleep "${AGENTBOX_AGENT_TOOL_POLL_SECONDS:-5}"`,
		`Retrying Agent tool installation from the sandbox cache: $LABEL`,
		`Agent tool installation timed out after ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800} seconds`,
		`tail -c 3000 "$1"`,
		`agent_tool_exec_logged "$CONTAINER" prerequisites sh -lc`,
		`NPM_PLAN=$(mktemp)`,
		`agent_tool_exec_logged "$CONTAINER" npm-packages sh -lc`,
		`npm install -g "$@"`,
		`Agent tool package batch installation failed`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite quiet Agent tool install is missing %q", expected)
		}
	}
	controlRecovery := strings.Index(workerDaemon, `BoxLite portal remained unavailable while polling Agent tool installation; waiting for the detached task before recovery`)
	sandboxRecovery := -1
	if controlRecovery >= 0 {
		sandboxRecovery = strings.Index(workerDaemon[controlRecovery:], `recover_boxlite_agent_tool_install "$CONTAINER" || return 1`)
	}
	if controlRecovery < 0 || sandboxRecovery < 0 {
		t.Fatal("BoxLite control service must be recovered before restarting the sandbox")
	}
}

func TestWorkerRestartsSandboxOnlyAfterInPlaceAgentUpdateRetryFails(t *testing.T) {
	for _, expected := range []string{
		`[ "${AGENTBOX_AGENT_TOOL_MODE:-ensure}" = upgrade ]`,
		`restarting the control service and sandbox before resuming`,
		`stop_boxlite_server
    ensure_boxlite_server || return 1`,
		`retrying without restarting the sandbox`,
		`recover_boxlite_agent_tool_install "$CONTAINER" || return 1`,
		`! docker exec "$CONTAINER" true >/dev/null 2>&1`,
		`正在恢复 BoxLite Agent 更新连接`,
		`VERSION_ATTEMPT=1`,
		`while [ "$VERSION_ATTEMPT" -le 3 ]`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("stable in-place Agent update is missing %q", expected)
		}
	}
}

func TestWorkerConfirmsSelfUpdateBeforeSlowRuntimeDiscovery(t *testing.T) {
	confirmation := strings.Index(workerDaemon, "  finalize_worker_update || FINALIZE_STATUS=$?\n  [ \"$FINALIZE_STATUS\" -ne 75 ] || exit 0\n  prepare_boxlite_config")
	imageRefresh := strings.Index(workerDaemon, "  refresh_boxlite_images || {")
	if confirmation < 0 || imageRefresh < 0 || confirmation > imageRefresh {
		t.Fatal("Worker self-update must be confirmed before BoxLite runtime discovery")
	}
}

func TestWorkerClaimsJobsEverySecond(t *testing.T) {
	start := strings.Index(workerDaemon, "run_worker()")
	if start < 0 {
		t.Fatal("Worker run loop was not found")
	}
	body := workerDaemon[start:]
	if !strings.Contains(body, "    else\n      sleep 1\n") {
		t.Fatal("Worker must claim queued Agent updates within one second")
	}
	if strings.Contains(body, "    sleep 5\n") {
		t.Fatal("Worker must not retain the five-second idle claim delay")
	}
}

func TestWorkerBlocksClaimsWhileUpdateJournalExists(t *testing.T) {
	start := strings.Index(workerDaemon, "run_worker() {")
	if start < 0 {
		t.Fatal("Worker run loop was not found")
	}
	body := workerDaemon[start:]
	finalize := strings.Index(body, "    finalize_worker_update || FINALIZE_STATUS=$?\n")
	blocked := strings.Index(body, `    if [ -e "$STATE_DIR/worker-update.json" ]; then`)
	claim := strings.Index(body, `"$SERVER_URL/api/servers/$SERVER_ID/jobs/claim"`)
	if finalize < 0 || blocked < 0 || claim < 0 || finalize >= blocked || blocked >= claim {
		t.Fatal("Worker must finalize or block on a durable update journal before claiming work")
	}
	if !strings.Contains(body[blocked:claim], "      sleep 2\n      continue\n") {
		t.Fatal("unresolved Worker update journal does not skip the claim loop")
	}
}

func TestWorkerEchoesLeaseGenerationOnProgressCompletionAndSelfUpdate(t *testing.T) {
	for _, expected := range []string{
		`.job.leaseGeneration // 0`,
		`{leaseGeneration:$leaseGeneration,stage:$stage,message:$message`,
		`{leaseGeneration:$leaseGeneration,success:$success,externalId:$externalId`,
		`.leaseGeneration // 0`,
		`jobId:$jobId,leaseGeneration:$leaseGeneration`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker lease fencing is missing %q", expected)
		}
	}
}

func TestWorkerKeepsPiInstallerAndCredentialSyntaxCompatible(t *testing.T) {
	for _, expected := range []string{
		`if [ "$MODE" = ensure ] && [ "$TOOL" != pi ] && docker exec "$CONTAINER" sh -lc '`,
		`VERSION=$(timeout 20 "$1" --version 2>&1 | head -n 1)`,
		`[ -n "$VERSION" ]`,
		`pi) INSTALL_PI=true; continue ;;`,
		`npm uninstall -g @mariozechner/pi-coding-agent`,
		`npm install -g --force @earendil-works/pi-coding-agent@latest`,
		`major === 22 && minor >= 19`,
		`apiKey: ("$AGENTBOX_KEY_" + (.id | env_id))`,
		`INSTALLER_REVISION=10`,
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

func TestWorkerChecksAndUpdatesSandboxAgentToolsInPlace(t *testing.T) {
	for _, expected := range []string{
		`check-sandbox-agent-tools)`,
		`update-sandbox-agent-tools)`,
		`check_agent_tools()`,
		`update_agent_tools()`,
		`.job.payload.requestedAgentTools[]?`,
		`AGENTBOX_AGENT_TOOL_MODE=upgrade`,
		`-name '\''.codex-*'\'' -exec rm -rf -- {} +`,
		`npm install -g "$@" || { npm cache clean --force`,
		`agent_tool_detect_version "$TARGET" "$TOOL_ID"`,
		`https://registry.npmjs.org/$PACKAGE`,
		`updated "Agent 工具已更新"`,
		`unchanged "更新命令已完成，但版本没有变化"`,
		`complete_agent_tool_job`,
		`agentTools:($agentTools[0] // [])`,
		`ERROR_DETAILS=${12:-}`,
		`[ -n "$ERROR_DETAILS" ] || ERROR_DETAILS='{}'`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("sandbox Agent tool lifecycle is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `${12:-{}}`) {
		t.Fatal("Worker result JSON must not use an ambiguous shell default containing braces")
	}
	if strings.Contains(workerDaemon, `npm uninstall -g @openai/codex`) {
		t.Fatal("Codex update must keep the working version until npm has installed its replacement")
	}
	if strings.Contains(workerDaemon, `docker rm -f "$TARGET"`) &&
		!strings.Contains(workerDaemon, `prepare_agent_tool_operation()`) {
		t.Fatal("Agent tool lifecycle must operate on the existing sandbox")
	}
}

func TestWorkerSerializesBoxLiteAgentUpdatesWithInteractiveSessions(t *testing.T) {
	for _, expected := range []string{
		`pause_session_worker()`,
		`resume_session_worker()`,
		`[ "$UPDATE_DRIVER" != boxlite ] || pause_session_worker
      if update_agent_tools`,
		`report_job_progress agent-tools-update "正在恢复 BoxLite 终端连接"`,
		`if [ "$UPDATE_ONLY_CODEX" = true ]; then`,
		`checkpoint_boxlite_agent_update "$UPDATE_TARGET"`,
		`recover_boxlite_agent_tool_install`,
		`UPDATE_TARGET=$(sandbox_container_name "$JOB_FILE")`,
		`"$UPDATE_TARGET"`,
		`resume_session_worker
      fi
      if [ "$UPDATE_OK" = true ]`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite Agent updates must serialize with interactive sessions: missing %q", expected)
		}
	}
	updateStart := strings.Index(workerDaemon, "    update-sandbox-agent-tools)")
	updateEnd := strings.Index(workerDaemon[updateStart:], "    update-worker)")
	if updateStart < 0 || updateEnd < 0 {
		t.Fatal("Agent update job boundary was not found")
	}
	updateBody := workerDaemon[updateStart : updateStart+updateEnd]
	successBranch := strings.Index(updateBody, `if [ "$UPDATE_OK" = true ]; then`)
	failureRecovery := strings.Index(updateBody, `elif ! recover_boxlite_agent_tool_install`)
	if successBranch < 0 || failureRecovery < 0 || successBranch >= failureRecovery {
		t.Fatal("successful BoxLite Agent updates must checkpoint before failure recovery")
	}
}

func TestWorkerCachesLatestAgentRuntimeImage(t *testing.T) {
	for _, expected := range []string{
		`prepare_agent_image()`,
		`AGENTBOX_AGENT_IMAGE_TTL_HOURS:-168`,
		`LABEL agentbox.runtime.cache=true`,
		`LABEL agentbox.runtime.refreshed=$NOW`,
		`cat > /opt/agentbox/agent-versions.json`,
		`Agent tool $TOOL was installed but command $COMMAND is unavailable`,
		`RUNTIME_IMAGE=$(prepare_agent_image "$IMAGE" "$JOB_FILE")`,
		`set -- "$@" "$RUNTIME_IMAGE"`,
		`Agent runtime refresh failed; using the last working cached image`,
		`BUILD_CONTAINER="agentbox-runtime-build-$CACHE_KEY-${JOB_ID:-manual}-${LEASE_GENERATION:-0}"`,
		`docker rm -f -v "$BUILD_CONTAINER"`,
		`command -v $COMMAND >/dev/null`,
		`curl --connect-timeout 15 --max-time 300`,
		`best_cached_agent_base()`,
		`CANDIDATE_INSTALLER_REVISION=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.installer-revision" }}' "$IMAGE_ID" 2>/dev/null || true)`,
		`BUILD_BASE_IMAGE=$(best_cached_agent_base "$BASE_IMAGE" "$TOOLS" "$NOW" "$TTL_SECONDS" "$INSTALLER_REVISION" "$DESKTOP")`,
		`report_job_progress agent-image "已命中 Agent 工具镜像缓存" hit exact-cache`,
		`report_job_progress agent-image "Agent 工具镜像缓存未命中，正在构建" miss "$CACHE_MISS_REASON"`,
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

func TestWorkerReportsProvisioningStagesWithoutBlockingJobs(t *testing.T) {
	for _, expected := range []string{
		`report_job_progress()`,
		`/jobs/$JOB_ID/progress`,
		`--data "$BODY" >/dev/null || true`,
		`report_job_progress runtime-check "正在检查 $DRIVER 运行时"`,
		`report_job_progress runtime-create "正在创建 Docker 沙箱实例"`,
		`report_job_progress configuration "正在写入沙箱配置"`,
		`report_job_progress setup "正在执行模板初始化命令"`,
		`report_job_progress verify "正在验证沙箱可用性"`,
		`report_agent_tool_progress()`,
		`--arg agentTool "$AGENT_TOOL" --arg agentToolStatus "$AGENT_TOOL_STATUS"`,
		`report_agent_tool_progress "$TOOL" running`,
		`report_agent_tool_progress "$TOOL" verifying`,
		`report_agent_tool_progress "$TOOL" succeeded`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker provisioning progress is missing %q", expected)
		}
	}
}

func TestWorkerReportsStructuredFailureMetadata(t *testing.T) {
	for _, expected := range []string{
		`complete_job_failure()`,
		`error:(if $success or $errorCode == "" then null else`,
		`{code:$errorCode,retryable:$errorRetryable,details:$errorDetails}`,
		`worker_error_stage()`,
		`runtime-probe|runtime-image|image-prepare|runtime-create`,
		`sandbox_create_failed`,
		`worker_update_rolled_back`,
		`worker_action_unsupported`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("structured Worker failure reporting is missing %q", expected)
		}
	}
}

func TestWorkerPreinstallsBoxLiteAgentToolsWithDockerCache(t *testing.T) {
	for _, expected := range []string{
		`AGENT_TOOLS_PREINSTALLED=false`,
		`if command -v docker >/dev/null`,
		`AGENTBOX_RUNTIME_DRIVER=docker`,
		`if ! RUNTIME_IMAGE=$(`,
		`prepare_agent_image "$IMAGE" "$JOB_FILE"`,
		`AGENT_TOOLS_PREINSTALLED=true`,
		`runtime_call "$DRIVER" prepare-image "$RUNTIME_IMAGE"`,
		`runtime_call "$DRIVER" create "$TARGET" "$RUNTIME_IMAGE"`,
		`if [ "$AGENT_TOOLS_PREINSTALLED" = true ]; then`,
		`cached image command is unavailable: $COMMAND`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite Docker Agent image reuse is missing %q", expected)
		}
	}
}

func TestWorkerBuildsAndStartsSandboxDesktopForEveryDriver(t *testing.T) {
	for _, expected := range []string{
		`install_desktop_runtime()`,
		`apt-get install -y --no-install-recommends xfce4 xfce4-terminal xvfb x11vnc dbus-x11 socat`,
		`Xvfb "$DISPLAY_NUMBER" -screen 0 1440x900x24 -nolisten tcp -ac`,
		`tr '\000' ' ' < "/proc/$PROCESS_PID/cmdline" | grep -Fq "$EXPECTED_COMMAND"`,
		`x11vnc -display "$DISPLAY_NUMBER" -rfbport 5900 -listen "$LISTEN_ADDRESS"`,
		`case "$LISTEN_ADDRESS" in 127.0.0.1|0.0.0.0)`,
		"' >&2; then",
		`LABEL agentbox.runtime.desktop=$DESKTOP`,
		`jq -e '.job.payload.desktop == true' "$JOB_FILE"`,
		`microsandbox_prepare_image "$RUNTIME_IMAGE"`,
		`ensure_desktop "$TARGET" "$JOB_FILE"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("sandbox desktop provisioning is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `printf '%s\n' 0.0.0.0`) {
		t.Fatal("sandbox desktop VNC listener must remain bound to loopback")
	}
}

func TestWorkerWritesSmallRuntimeFilesThroughStdin(t *testing.T) {
	for _, expected := range []string{
		`agent_tool_exec "$CONTAINER" rm -rf /opt/agentbox/agent-versions.json`,
		`agent_tool_exec_stdin "$CONTAINER" "$VERSION_FILE" sh -c 'cat > /opt/agentbox/agent-versions.json'`,
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

func TestWorkerStartRetriesSandboxCreationAfterProvisioningFailure(t *testing.T) {
	start := strings.Index(workerDaemon, "start_sandbox()")
	if start < 0 {
		t.Fatal("sandbox start function was not found")
	}
	restart := strings.Index(workerDaemon[start:], "restart_sandbox()")
	if restart < 0 {
		t.Fatal("sandbox restart function was not found")
	}
	body := workerDaemon[start : start+restart]
	for _, expected := range []string{
		`EXTERNAL_ID=$(jq -r '.job.payload.externalId // empty' "$JOB_FILE")`,
		`if [ -z "$EXTERNAL_ID" ]; then`,
		`create_sandbox "$JOB_FILE"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("sandbox start retry is missing %q", expected)
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
		`index("deepseek-harness")`,
		`append_env "$ENV_FILE" DSH_PERMISSION_MODE danger-full-access`,
		`"agent-default-model": {`,
		`"llm-pi-ai": {`,
		`api: "openai-completions"`,
		`(.chatEndpoint | rtrimstr("/"))`,
		`cat > /root/.dsh/settings.yaml`,
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
		`GROK_CREDENTIAL=$(jq -c '`,
		`GROK_ENDPOINT=$(printf '%s' "$GROK_CREDENTIAL" | jq -r '.openaiEndpoint')`,
		`cat > /root/.grok/config.toml`,
		`default = "agentbox"`,
		`append_env "$ENV_FILE" QODERCN_PERSONAL_ACCESS_TOKEN "$SECRET"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("extended Agent credential config is missing %q", expected)
		}
	}
}

func TestWorkerConfiguresDeepSeekHarnessMCPBeforeLaunch(t *testing.T) {
	for _, expected := range []string{
		`else "abx_" + $raw[:13] + "_" + $raw[-14:]`,
		`DSH MCP server names collide after normalization`,
		`name: \"@deepseek-ai/dsh-mcp-client\"`,
		`transport: streamable-http`,
		`cat > /opt/agentbox/dsh-mcp.patch.yml`,
		`exec /usr/bin/dsh --patch /opt/agentbox/dsh-mcp.patch.yml "$@"`,
		`plugin|-V|--version|-h|--help|--dump-default-config`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("DeepSeek Harness MCP integration is missing %q", expected)
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

func TestWorkerSkillInstallDoesNotReuseRuntimeTargetVariable(t *testing.T) {
	for _, expected := range []string{
		`for SKILL_TARGET do`,
		`cp -R "$SKILL_SOURCE/." "$SKILL_TARGET/"`,
		`' agentbox "/opt/agentbox/skills/$ID"`,
		`sh -c 'cat > "$1"' agentbox "/opt/agentbox/skills/$ID/SKILL.md"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("sandbox skill installation is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `sh -c "cat > $SKILL_TARGET/SKILL.md"`) ||
		strings.Contains(workerDaemon, `sh -c "cat > /opt/agentbox/skills/$ID/SKILL.md"`) {
		t.Fatal("sandbox skill installation interpolates the skill ID into the remote shell command")
	}
	if strings.Contains(workerDaemon, `for TARGET in \
      "/root/.agents/skills/$ID"`) {
		t.Fatal("sandbox skill installation reuses the runtime TARGET variable")
	}
}

func TestWorkerInstallerIncludesInteractiveSessionDaemon(t *testing.T) {
	for _, expected := range []string{
		`/api/worker/agentbox-worker?arch=$ARCH`,
		`install -m 0755 "$worker_tmp" /usr/local/bin/agentbox-worker`,
		`install_host_dependencies`,
		`migrate_existing_config`,
		`if ! go mod tidy; then`,
		`GOPROXY="${AGENTBOX_GOPROXY:-https://goproxy.cn,direct}" go mod tidy`,
		`printf '%s\n%s\n%s\n' "$SERVER_URL" "$SERVER_ID" "$CREDENTIAL" > "$CONFIG"`,
		`systemctl restart agentbox-worker.service`,
		`worker_origin_curl -fsS -D "$worker_headers_tmp" "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH" -o "$worker_tmp"`,
		`verify_worker_checksum "$worker_tmp" "$worker_headers_tmp"`,
		`the server did not provide a Worker checksum; refusing to install`,
		`SERVER_URL=$(normalize_worker_origin "${1:-}")`,
		`Plain HTTP is allowed only for an exact localhost or loopback IP`,
		`--proto '=https' --proto-redir '=https'`,
		`command -v flock >/dev/null`,
		`command -v sync >/dev/null`,
		`PACKAGES="ca-certificates jq util-linux"`,
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

func TestWorkerSupervisesInteractiveSessionProcess(t *testing.T) {
	for _, expected := range []string{
		`ensure_session_worker()`,
		`kill -0 "$SESSION_PID" >/dev/null 2>&1`,
		`SESSION_STATE=$(ps -o stat= -p "$SESSION_PID" 2>/dev/null`,
		`''|Z*) ;;`,
		`wait "$SESSION_PID" >/dev/null 2>&1 || true`,
		`/usr/local/bin/agentbox-worker session "$CONFIG" &`,
		`SESSION_PID=$!`,
		`[ -e "$STATE_DIR/worker-update.json" ] || ensure_session_worker`,
		"  while :; do\n    FINALIZE_STATUS=0\n    finalize_worker_update || FINALIZE_STATUS=$?\n    [ \"$FINALIZE_STATUS\" -ne 75 ] || exit 0\n    if [ -e \"$STATE_DIR/worker-update.json\" ]; then",
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("interactive session supervision is missing %q", expected)
		}
	}
}

func TestWorkerStartsInteractiveSessionBeforeSlowImageInventory(t *testing.T) {
	start := strings.Index(workerDaemon, "run_worker()")
	if start < 0 {
		t.Fatal("worker run loop was not found")
	}
	body := workerDaemon[start:]
	sessionIndex := strings.Index(body, `[ -e "$STATE_DIR/worker-update.json" ] || ensure_session_worker`)
	inventoryIndex := strings.Index(body, "  refresh_boxlite_images || {")
	if sessionIndex < 0 || inventoryIndex < 0 || sessionIndex > inventoryIndex {
		t.Fatal("interactive sessions must start before the slow BoxLite image inventory refresh")
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
		`/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous`,
		`mv -f "$NEXT" /usr/local/bin/agentbox-worker`,
		`mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver`,
		`formatVersion:2,phase:"prepared"`,
		`driverUpdated:$driverUpdated,driverHadPrevious:$driverHadPrevious`,
		`workerChecksum:$workerChecksum,workerPreviousChecksum:$workerPreviousChecksum`,
		`driverPreviousChecksum:$driverPreviousChecksum`,
		`OnActiveSec=120s`,
		`WantedBy=timers.target`,
		`systemctl enable --now "$GUARDIAN_TIMER"`,
		`flock -x 9`,
		`[ "${AGENTBOX_WORKER_VERSION:-unknown}" = "$TARGET_VERSION" ] || exit 0`,
		`worker_update_lease_keepalive &`,
		`.job.payload.previousVersion // empty`,
		`ExecStartPre=/var/lib/agentbox-worker/worker-update-start-guard.sh`,
		`refusing to start the previous job loop`,
		`KillMode=control-group`,
		`TimeoutStopSec=20s`,
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

func TestWorkerRejectsQueuedPreviousVersionDriftBeforeActivation(t *testing.T) {
	start := strings.Index(workerDaemon, "update_worker() (")
	if start < 0 {
		t.Fatal("Worker self-update function was not found")
	}
	body := workerDaemon[start:]
	queued := strings.Index(body, `EXPECTED_PREVIOUS_VERSION=$(jq -r '.job.payload.previousVersion // empty'`)
	check := strings.Index(body, `[ "$PREVIOUS_VERSION" != "$EXPECTED_PREVIOUS_VERSION" ]`)
	backup := strings.Index(body, `install -m 0755 /usr/local/bin/agentbox-worker "$PREVIOUS_TMP"`)
	activate := strings.Index(body, `mv -f "$NEXT" /usr/local/bin/agentbox-worker`)
	if queued < 0 || check < 0 || backup < 0 || activate < 0 || queued >= check || check >= backup || backup >= activate {
		t.Fatal("queued previousVersion must be checked before backing up or activating the Worker")
	}
}

func TestWorkerSelfUpdateVerifiesChecksumBeforeExecutingDownload(t *testing.T) {
	start := strings.Index(workerDaemon, "update_worker()")
	if start < 0 {
		t.Fatal("Worker self-update function was not found")
	}
	end := strings.Index(workerDaemon[start:], `refresh_microsandbox_driver "$DRIVER_NEXT" ||`)
	if end < 0 {
		t.Fatal("Worker self-update download stage was not found")
	}
	body := workerDaemon[start : start+end]
	for _, expected := range []string{
		`worker_origin_curl_follow -fsS -D "$WORKER_HEADERS"`,
		`EXPECTED_SHA256=$(tr -d '\r' < "$WORKER_HEADERS"`,
		`the server did not provide a Worker checksum`,
		`ACTUAL_SHA256=$(worker_update_sha256 "$WORKER_TMP")`,
		`downloaded Worker checksum mismatch`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Worker self-update checksum verification is missing %q", expected)
		}
	}
	checksumIndex := strings.Index(body, `ACTUAL_SHA256=$(worker_update_sha256 "$WORKER_TMP")`)
	versionIndex := strings.Index(body, `DOWNLOADED_VERSION=$("$WORKER_TMP" version`)
	if checksumIndex < 0 || versionIndex < 0 || checksumIndex >= versionIndex {
		t.Fatal("Worker self-update must verify the checksum before executing the downloaded binary")
	}
}

func TestWorkerSelfUpdateConfirmsOnlyAfterServerAcceptsReport(t *testing.T) {
	start := strings.Index(workerDaemon, "finalize_worker_update()")
	if start < 0 {
		t.Fatal("Worker update finalization function was not found")
	}
	end := strings.Index(workerDaemon[start:], "restore_worker_binary()")
	if end < 0 {
		t.Fatal("Worker update finalization function end was not found")
	}
	body := workerDaemon[start : start+end]
	reportIndex := strings.Index(body, `if ! complete_job "$JOB_ID" true "" "Worker 已更新到 $TARGET_VERSION"; then`)
	confirmIndex := strings.Index(body, `mv -f "$CONFIRMED_TMP" "$CONFIRMED"`)
	cancelIndex := strings.Index(body, `cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE"`)
	journalCleanupIndex := strings.Index(body, `rm -f "$MARKER" || exit 1`)
	workerCommitIndex := strings.Index(body, `/usr/local/lib/agentbox/.agentbox-worker.previous &&`)
	driverCommitIndex := strings.Index(body, `/usr/local/lib/agentbox/.agentbox-microsandbox-driver.previous &&`)
	if reportIndex < 0 || confirmIndex < 0 || cancelIndex < 0 || journalCleanupIndex < 0 ||
		workerCommitIndex < 0 || driverCommitIndex < 0 || reportIndex >= confirmIndex ||
		confirmIndex >= journalCleanupIndex || journalCleanupIndex >= cancelIndex ||
		cancelIndex >= workerCommitIndex || workerCommitIndex >= driverCommitIndex {
		t.Fatal("Server confirmation, journal removal, guardian cleanup and fallback refresh are out of order")
	}
	if !strings.Contains(workerDaemon, `rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed"`) {
		t.Fatal("the scheduled rollback check must clean confirmation state after accepting the update")
	}
}

func TestWorkerRollbackReportsBeforeStartingPreviousBinary(t *testing.T) {
	start := strings.Index(workerDaemon, "finalize_worker_update() (")
	end := strings.Index(workerDaemon[start:], "restore_worker_binary()")
	if start < 0 || end < 0 {
		t.Fatal("Worker update finalizer was not found")
	}
	body := workerDaemon[start : start+end]
	oldStart := strings.Index(body, "    old)\n")
	mixedStart := strings.Index(body, "    mixed)\n")
	if oldStart < 0 || mixedStart < 0 || oldStart >= mixedStart {
		t.Fatal("Worker rollback branches were not found")
	}
	oldBody := body[oldStart:mixedStart]
	report := strings.Index(oldBody, `complete_job_failure "$JOB_ID" worker_update_rolled_back`)
	removeMarker := strings.Index(oldBody, `rm -f "$MARKER" || exit 1`)
	exitForRestart := strings.Index(oldBody, `] || exit 75`)
	if report < 0 || removeMarker < 0 || exitForRestart < 0 || report >= removeMarker || removeMarker >= exitForRestart {
		t.Fatal("rollback must be accepted and its journal cleared before the new process exits into the previous binary")
	}
	if strings.Contains(body[mixedStart:], `schedule_worker_restart "$JOB_ID"`) {
		t.Fatal("mixed-pair recovery must not start the previous binary before rollback reconciliation")
	}
}

func TestWorkerSelfUpdateStagesAndRollsBackMicrosandboxDriverWithWorker(t *testing.T) {
	for _, expected := range []string{
		`refresh_microsandbox_driver "$DRIVER_NEXT"`,
		`install -m 0755 "$BUILD_DIR/agentbox-microsandbox-driver" "$DRIVER_NEXT"`,
		`--argjson driverUpdated "$DRIVER_UPDATED"`,
		`--argjson driverHadPrevious "$DRIVER_HAD_PREVIOUS"`,
		`--arg driverPrevious "$DRIVER_PREVIOUS"`,
		`--arg driverChecksum "$DRIVER_CHECKSUM"`,
		`restore_worker_update "$WORKER_PREVIOUS" "$DRIVER_PREVIOUS"`,
		`restore_microsandbox_driver "$DRIVER_PREVIOUS" "$DRIVER_HAD_PREVIOUS"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("atomic Worker/driver update is missing %q", expected)
		}
	}

	stageStart := strings.Index(workerDaemon, "refresh_microsandbox_driver() (")
	updateStart := strings.Index(workerDaemon, "update_worker() (")
	if stageStart < 0 || updateStart < 0 || stageStart >= updateStart {
		t.Fatal("Microsandbox driver staging function boundary was not found")
	}
	stageBody := workerDaemon[stageStart:updateStart]
	if strings.Contains(stageBody, `mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver`) {
		t.Fatal("Microsandbox driver staging must not replace the active driver")
	}

	updateBody := workerDaemon[updateStart:]
	guardianInstallIndex := strings.Index(updateBody, `systemctl enable --now "$GUARDIAN_TIMER"`)
	markerPublishIndex := strings.Index(updateBody, `mv -f "$MARKER_TMP" "$MARKER"`)
	rollbackIndex := strings.Index(updateBody, `! systemctl restart "$GUARDIAN_TIMER"`)
	workerActivateIndex := strings.Index(updateBody, `mv -f "$NEXT" /usr/local/bin/agentbox-worker`)
	driverActivateIndex := strings.Index(updateBody, `mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver`)
	restartIndex := strings.Index(updateBody, `schedule_worker_restart "$JOB_ID"`)
	if guardianInstallIndex < 0 || markerPublishIndex < 0 || rollbackIndex < 0 || workerActivateIndex < 0 ||
		driverActivateIndex < 0 || restartIndex < 0 || guardianInstallIndex >= markerPublishIndex ||
		markerPublishIndex >= rollbackIndex || rollbackIndex >= workerActivateIndex ||
		workerActivateIndex >= driverActivateIndex || driverActivateIndex >= restartIndex {
		t.Fatal("guardian installation, journal publication, arming, pair activation and restart are out of order")
	}
	if got := strings.Count(updateBody[:restartIndex+1], `abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS"`); got < 2 {
		t.Fatalf("pre-restart activation failures covered by paired rollback = %d, want at least 2", got)
	}
	if !strings.Contains(updateBody, `if [ -x /usr/local/bin/agentbox-microsandbox-driver ]; then`) ||
		!strings.Contains(updateBody, `rm -f "$DRIVER_PREVIOUS_TMP" "$DRIVER_PREVIOUS" || {`) {
		t.Fatal("every update generation must record whether a previous Microsandbox driver existed")
	}
	abortStart := strings.Index(workerDaemon, "abort_worker_update() (")
	recoverStart := strings.Index(workerDaemon, "recover_worker_update() (")
	if abortStart < 0 || recoverStart < 0 || abortStart >= recoverStart {
		t.Fatal("Worker update abort helper was not found")
	}
	abortBody := workerDaemon[abortStart:recoverStart]
	validateFallback := strings.Index(abortBody, `worker_update_fallback_valid "$MARKER"`)
	restoreFallback := strings.Index(abortBody, `restore_worker_update "$WORKER_PREVIOUS" "$DRIVER_PREVIOUS"`)
	if validateFallback < 0 || restoreFallback < 0 || validateFallback >= restoreFallback {
		t.Fatal("activation abort must validate the complete fallback pair before mutating either active path")
	}
}

func TestWorkerUpdateJournalIsNeverPublishedWithInactiveGuardian(t *testing.T) {
	updateStart := strings.Index(workerDaemon, "update_worker() (")
	if updateStart < 0 {
		t.Fatal("Worker self-update function was not found")
	}
	updateBody := workerDaemon[updateStart:]
	startGuardian := strings.Index(updateBody, `systemctl enable --now "$GUARDIAN_TIMER"`)
	publishJournal := strings.Index(updateBody, `mv -f "$MARKER_TMP" "$MARKER"`)
	resetDeadline := strings.Index(updateBody, `systemctl restart "$GUARDIAN_TIMER"`)
	if startGuardian < 0 || publishJournal < 0 || resetDeadline < 0 ||
		startGuardian >= publishJournal || publishJournal >= resetDeadline {
		t.Fatal("the rollback timer must already be active when the durable update journal is published")
	}

	const rollbackStart = "  cat > \"$ROLLBACK_SCRIPT\" <<'ROLLBACK'\n"
	guardianStart := strings.Index(workerDaemon, rollbackStart)
	if guardianStart < 0 {
		t.Fatal("persistent rollback guardian was not found")
	}
	guardianStart += len(rollbackStart)
	guardianEnd := strings.Index(workerDaemon[guardianStart:], "\nROLLBACK\n")
	if guardianEnd < 0 {
		t.Fatal("persistent rollback guardian end was not found")
	}
	guardian := workerDaemon[guardianStart : guardianStart+guardianEnd]
	noMarkerStart := strings.Index(guardian, `[ -e "$MARKER" ] || {`)
	noMarkerEnd := strings.Index(guardian[noMarkerStart:], "\n}\n[ -s \"$MARKER\" ]")
	if noMarkerStart < 0 || noMarkerEnd < 0 {
		t.Fatal("guardian no-journal failpoint branch was not found")
	}
	noMarker := guardian[noMarkerStart : noMarkerStart+noMarkerEnd]
	if !strings.Contains(noMarker, "  cleanup_guardian") || !strings.Contains(noMarker, "  exit 0") ||
		strings.Contains(noMarker, "systemctl start agentbox-worker.service") || strings.Contains(noMarker, `rm -f "$MARKER"`) {
		t.Fatal("a guardian that owns an abandoned pre-publication update must clean up without starting or mutating the Worker")
	}
}

func TestWorkerUpdatePersistsRecoveryBeforeActivePairMutation(t *testing.T) {
	updateStart := strings.Index(workerDaemon, "update_worker() (")
	updateEnd := strings.Index(workerDaemon[updateStart:], "\nagent_tool_command() {")
	if updateStart < 0 || updateEnd < 0 {
		t.Fatal("Worker self-update function boundary was not found")
	}
	updateBody := workerDaemon[updateStart : updateStart+updateEnd]
	fallback := strings.Index(updateBody, `mv -f "$PREVIOUS_TMP" "$PREVIOUS"`)
	persistFallback := strings.Index(updateBody, `worker_update_sync /usr/local/lib/agentbox`)
	armGuardian := strings.Index(updateBody, `systemctl enable --now "$GUARDIAN_TIMER"`)
	publishJournal := strings.Index(updateBody, `mv -f "$MARKER_TMP" "$MARKER"`)
	persistGuardianRelative := -1
	if armGuardian >= 0 {
		persistGuardianRelative = strings.Index(updateBody[armGuardian:], `worker_update_sync /etc/systemd/system`)
	}
	if fallback < 0 || persistFallback < 0 || armGuardian < 0 || publishJournal < 0 || persistGuardianRelative < 0 ||
		fallback >= persistFallback || persistFallback >= armGuardian ||
		armGuardian+persistGuardianRelative >= publishJournal {
		t.Fatal("fallback pair and active guardian must be durable before journal publication")
	}
	persistJournalRelative := strings.Index(updateBody[publishJournal:], `worker_update_sync "$STATE_DIR"`)
	workerActivate := strings.Index(updateBody, `mv -f "$NEXT" /usr/local/bin/agentbox-worker`)
	driverActivate := strings.Index(updateBody, `mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver`)
	if persistJournalRelative < 0 || workerActivate < 0 || driverActivate < 0 {
		t.Fatal("journal or active pair durability boundary was not found")
	}
	persistJournal := publishJournal + persistJournalRelative
	persistActiveRelative := strings.Index(updateBody[driverActivate:], `worker_update_sync /usr/local/bin "$STATE_DIR"`)
	restart := strings.Index(updateBody, `schedule_worker_restart "$JOB_ID"`)
	if persistActiveRelative < 0 || restart < 0 || persistJournal >= workerActivate ||
		workerActivate >= driverActivate || driverActivate+persistActiveRelative >= restart {
		t.Fatal("journal must persist before pair mutation and the pair must persist before restart")
	}
	if !strings.Contains(workerDaemon, `sync -f "$SYNC_PATH" 2>/dev/null`) {
		t.Fatal("Worker update durability helper must flush every referenced filesystem")
	}
}

func TestWorkerUpdateFencesInteractiveSessionsDuringPairMutation(t *testing.T) {
	updateStart := strings.Index(workerDaemon, "update_worker() (")
	if updateStart < 0 {
		t.Fatal("Worker self-update function was not found")
	}
	updateBody := workerDaemon[updateStart:]
	pause := strings.Index(updateBody, `if ! pause_session_worker; then`)
	workerActivate := strings.Index(updateBody, `mv -f "$NEXT" /usr/local/bin/agentbox-worker`)
	driverActivate := strings.Index(updateBody, `mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver`)
	if pause < 0 || workerActivate < 0 || driverActivate < 0 || pause >= workerActivate || workerActivate >= driverActivate {
		t.Fatal("interactive sessions must stop before either active executable changes")
	}
	guardianStart := strings.Index(workerDaemon, `cat > "$ROLLBACK_SCRIPT" <<'ROLLBACK'`)
	guardianEnd := strings.Index(workerDaemon[guardianStart:], "\nROLLBACK\n")
	if guardianStart < 0 || guardianEnd < 0 {
		t.Fatal("persistent rollback guardian was not found")
	}
	guardian := workerDaemon[guardianStart : guardianStart+guardianEnd]
	stop := strings.Index(guardian, "systemctl stop agentbox-worker.service\n")
	restore := strings.Index(guardian, `WORKER_RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore`)
	if stop < 0 || restore < 0 || stop >= restore {
		t.Fatal("rollback guardian must stop the service cgroup before restoring the pair")
	}
}

func TestWorkerManualRecoveryHandlesPhysicallyInvalidJournal(t *testing.T) {
	recoveryStart := strings.Index(workerDaemon, "recover_worker_update() (")
	recoveryEnd := strings.Index(workerDaemon[recoveryStart:], "\nrefresh_microsandbox_driver() (")
	if recoveryStart < 0 || recoveryEnd < 0 {
		t.Fatal("manual Worker recovery function was not found")
	}
	recovery := workerDaemon[recoveryStart : recoveryStart+recoveryEnd]
	if !strings.Contains(recovery,
		`if worker_update_marker_valid "$MARKER" && worker_update_fallback_valid "$MARKER"; then`) {
		t.Fatal("shape-valid journals with impossible fallback checksums need a manual recovery path")
	}
	stop := strings.Index(recovery, `systemctl stop agentbox-worker.service`)
	restore := strings.Index(recovery, `restore_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS"`)
	if stop < 0 || restore < 0 || stop >= restore {
		t.Fatal("manual recovery must stop sessions before restoring the fallback pair")
	}
}

func TestWorkerRefreshesAnExistingMicrosandboxDriverWithoutKVM(t *testing.T) {
	refreshStart := strings.Index(workerDaemon, "refresh_microsandbox_driver() (")
	updateStart := strings.Index(workerDaemon, "update_worker() (")
	if refreshStart < 0 || updateStart < 0 || refreshStart >= updateStart {
		t.Fatal("Microsandbox driver refresh function was not found")
	}
	refresh := workerDaemon[refreshStart:updateStart]
	for _, expected := range []string{
		`[ ! -c /dev/kvm ]`,
		`[ ! -e /usr/local/bin/agentbox-microsandbox-driver ]`,
		`[ ! -L /usr/local/bin/agentbox-microsandbox-driver ]`,
	} {
		if !strings.Contains(refresh, expected) {
			t.Fatalf("existing driver refresh without KVM is missing %q", expected)
		}
	}
	if strings.Contains(refresh, `[ -c /dev/kvm ] || exit 0`) {
		t.Fatal("temporary KVM loss must not silently retain an old installed driver")
	}
}

func TestWorkerUpdateRecoveryOwnsCompletionWhileJournalExists(t *testing.T) {
	start := strings.Index(workerDaemon, "    update-worker)\n")
	if start < 0 {
		t.Fatal("Worker update dispatch branch was not found")
	}
	end := strings.Index(workerDaemon[start:], "    configure-sandbox-proxy)\n")
	if end < 0 {
		t.Fatal("Worker update dispatch branch end was not found")
	}
	body := workerDaemon[start : start+end]
	journal := strings.Index(body, `if [ -e "$STATE_DIR/worker-update.json" ]; then`)
	genericCompletion := strings.Index(body, `complete_job_failure "$JOB_ID" worker_update_failed`)
	if journal < 0 || genericCompletion < 0 || journal >= genericCompletion {
		t.Fatal("a durable Worker update journal must suppress the generic failure completion")
	}
}

func TestWorkerUpdateGuardianNeverStopsItselfWhileHoldingSharedLock(t *testing.T) {
	start := strings.Index(workerDaemon, "cleanup_worker_update_guardian() {")
	if start < 0 {
		t.Fatal("Worker update guardian cleanup helper was not found")
	}
	end := strings.Index(workerDaemon[start:], "schedule_worker_restart() {")
	if end < 0 {
		t.Fatal("Worker update guardian cleanup helper end was not found")
	}
	body := workerDaemon[start : start+end]
	if strings.Contains(body, `systemctl stop "$GUARDIAN_SERVICE"`) {
		t.Fatal("Worker finalizer may deadlock by stopping a guardian waiting on its shared lock")
	}
	if !strings.Contains(body, `systemctl disable --now "$GUARDIAN_TIMER"`) {
		t.Fatal("Worker finalizer must be able to disable the persistent rollback timer")
	}
	if !strings.Contains(body, `systemctl disable --now "$GUARDIAN_TIMER" >/dev/null 2>&1 || true`) ||
		!strings.Contains(body, "  return 0\n") {
		t.Fatal("Worker guardian cleanup must be idempotent when a unit was never installed")
	}
}

func TestWorkerRecoversLegacyMixedWorkerDriverPair(t *testing.T) {
	stateStart := strings.Index(workerDaemon, "worker_update_pair_state() (")
	fallbackStart := strings.Index(workerDaemon, "worker_update_fallback_valid() (")
	finalizeStart := strings.Index(workerDaemon, "finalize_worker_update() (")
	if stateStart < 0 || fallbackStart < 0 || finalizeStart < 0 || stateStart >= fallbackStart {
		t.Fatal("Worker update recovery functions were not found")
	}
	stateBody := workerDaemon[stateStart:fallbackStart]
	for _, expected := range []string{
		`worker_update_driver_matches_legacy "$DRIVER_UPDATED" "$DRIVER_CHECKSUM" && DRIVER_NEW=true`,
		`worker_update_driver_previous_matches_legacy "$MARKER" && DRIVER_OLD=true`,
		`[ "$WORKER_OLD" = true ] && [ "$DRIVER_OLD" = true ]`,
	} {
		if !strings.Contains(stateBody, expected) {
			t.Fatalf("legacy physical pair classification is missing %q", expected)
		}
	}
	finalizeBody := workerDaemon[finalizeStart:]
	mixed := strings.Index(finalizeBody, "    mixed)\n")
	if mixed < 0 {
		t.Fatal("mixed Worker update recovery branch was not found")
	}
	mixedBody := finalizeBody[mixed:]
	if !strings.Contains(mixedBody, `worker_update_fallback_valid "$MARKER"`) ||
		!strings.Contains(mixedBody, `restore_worker_update "$WORKER_PREVIOUS" "$DRIVER_PREVIOUS"`) {
		t.Fatal("legacy and current mixed pairs must validate their complete fallback before restoration")
	}
	mixedEnd := strings.Index(mixedBody, ";;\n")
	if mixedEnd < 0 {
		t.Fatal("mixed Worker update recovery branch end was not found")
	}
	if strings.Contains(mixedBody[:mixedEnd], `[ "$FORMAT_VERSION" = 2 ]`) {
		t.Fatal("legacy mixed pairs must not be left unrecoverable by a format-2-only gate")
	}
}

func TestWorkerExposesAndRepairsCorruptUpdateJournal(t *testing.T) {
	for _, expected := range []string{
		`worker-update-pending`,
		`worker-update-degraded`,
		`(.workerPreviousVersion | version)`,
		`(.rollbackScript == "/var/lib/agentbox-worker/worker-update-rollback.sh")`,
		`agentbox-worker recover-update --restore-previous`,
		`recover-update) shift; recover_worker_update "$@"`,
		`QUARANTINE="$STATE_DIR/worker-update.corrupt.$(date +%s).$$.json"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("corrupt Worker update recovery is missing %q", expected)
		}
	}
	recoveryStart := strings.Index(workerDaemon, "recover_worker_update() (")
	refreshStart := strings.Index(workerDaemon, "refresh_microsandbox_driver() (")
	if recoveryStart < 0 || refreshStart < 0 || recoveryStart >= refreshStart {
		t.Fatal("manual Worker update recovery function was not found")
	}
	body := workerDaemon[recoveryStart:refreshStart]
	quarantine := strings.Index(body, `mv "$MARKER" "$QUARANTINE"`)
	unlock := strings.Index(body, `flock -u 9`)
	restart := strings.Index(body, `systemctl restart agentbox-worker.service`)
	if quarantine < 0 || unlock < 0 || restart < 0 || quarantine >= unlock || unlock >= restart {
		t.Fatal("manual recovery must quarantine the corrupt journal and release its lock before restart")
	}
	finalizeStart := strings.Index(workerDaemon, "finalize_worker_update() (")
	finalizeEnd := strings.Index(workerDaemon[finalizeStart:], "restore_worker_binary()")
	if finalizeStart < 0 || finalizeEnd < 0 {
		t.Fatal("Worker update finalizer was not found")
	}
	finalizeBody := workerDaemon[finalizeStart : finalizeStart+finalizeEnd]
	validate := strings.Index(finalizeBody, `worker_update_marker_valid "$MARKER" || exit 1`)
	classify := strings.Index(finalizeBody, `PAIR_STATE=$(worker_update_pair_state "$MARKER")`)
	if validate < 0 || classify < 0 || validate >= classify {
		t.Fatal("corrupt Worker update journals must be rejected before physical pair classification")
	}
}

func TestWorkerUpdateGuardianClearsJournalBeforeDisablingItself(t *testing.T) {
	start := strings.Index(workerDaemon, `cat > "$ROLLBACK_SCRIPT" <<'ROLLBACK'`)
	if start < 0 {
		t.Fatal("persistent rollback guardian was not found")
	}
	end := strings.Index(workerDaemon[start:], "\nROLLBACK\n")
	if end < 0 {
		t.Fatal("persistent rollback guardian end was not found")
	}
	body := workerDaemon[start : start+end]
	removeMarker := strings.Index(body, `rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed"`)
	if removeMarker < 0 {
		t.Fatal("confirmed rollback guardian marker cleanup was not found")
	}
	disableGuardian := strings.Index(body[removeMarker:], "  cleanup_guardian\n")
	if disableGuardian < 0 {
		t.Fatal("confirmed rollback guardian cleanup sequence was not found")
	}
	rollbackPhase := strings.Index(body, `PHASE_TMP="$MARKER.phase"`)
	if rollbackPhase < 0 {
		t.Fatal("rollback guardian phase journal was not found")
	}
	rollbackBody := body[rollbackPhase:]
	report := strings.Index(rollbackBody, "report_rollback\n")
	stopWorker := strings.Index(body, "systemctl stop agentbox-worker.service\n")
	restoreWorker := strings.Index(body, `WORKER_RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore`)
	clearJournal := strings.Index(rollbackBody, `rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed"`)
	unlock := strings.Index(rollbackBody, "flock -u 9\n")
	startWorker := strings.Index(rollbackBody, "systemctl start agentbox-worker.service || exit 1\n")
	if report < 0 || stopWorker < 0 || restoreWorker < 0 || clearJournal < 0 || unlock < 0 || startWorker < 0 ||
		stopWorker >= restoreWorker || restoreWorker >= rollbackPhase || report >= clearJournal ||
		clearJournal >= unlock || unlock >= startWorker {
		t.Fatal("rollback guardian must stop, reconcile, clear, unlock and start the previous Worker in order")
	}
}

func TestWorkerUpdateRollbackAttemptsBothBinaries(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	start := strings.Index(workerDaemon, "restore_worker_binary() {")
	end := strings.Index(workerDaemon, "abort_worker_update() (")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("Worker update restore helpers were not found")
	}
	restoreHelpers := workerDaemon[start:end]
	mock := `install() {
  printf 'install:%s\n' "$3"
  [ "$FAIL_SOURCE" != "$3" ]
}
mv() { printf 'move:%s\n' "$2"; }
rm() { printf 'remove:%s\n' "$*"; }
worker_update_sync() { :; }
`
	for _, test := range []struct {
		name, failSource, driverHadPrevious string
		wantFailure                         bool
	}{
		{name: "both restored", driverHadPrevious: "true"},
		{name: "worker restore fails", failSource: "worker.previous", driverHadPrevious: "true", wantFailure: true},
		{name: "driver restore fails", failSource: "driver.previous", driverHadPrevious: "true", wantFailure: true},
		{name: "new driver is removed", driverHadPrevious: "false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "FAIL_SOURCE="+test.failSource,
				"DRIVER_HAD_PREVIOUS="+test.driverHadPrevious)
			command.Stdin = strings.NewReader("set -u\n" + mock + restoreHelpers +
				`restore_worker_update worker.previous driver.previous true "$DRIVER_HAD_PREVIOUS"` + "\n")
			output, err := command.CombinedOutput()
			if test.wantFailure == (err == nil) {
				t.Fatalf("restore result = (%q, %v), want failure %t", output, err, test.wantFailure)
			}
			if !strings.Contains(string(output), "install:worker.previous") {
				t.Fatalf("Worker restore was not attempted: %q", output)
			}
			if test.driverHadPrevious == "true" && !strings.Contains(string(output), "install:driver.previous") {
				t.Fatalf("driver restore was not attempted after Worker result: %q", output)
			}
			if test.driverHadPrevious == "false" && !strings.Contains(string(output), "agentbox-microsandbox-driver") {
				t.Fatalf("new driver removal was not attempted: %q", output)
			}
		})
	}
}

func TestWorkerUpdateClassifiesPhysicalWorkerDriverPair(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is not available on this test host")
	}
	start := strings.Index(workerDaemon, "worker_update_sha256() {")
	end := strings.Index(workerDaemon, "update_worker_marker_phase() {")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("Worker update pair classification helpers were not found")
	}
	helpers := workerDaemon[start:end]
	helpers = strings.ReplaceAll(helpers, "/usr/local/bin/agentbox-worker", `"$TEST_WORKER_ACTIVE"`)
	helpers = strings.ReplaceAll(helpers, "/usr/local/bin/agentbox-microsandbox-driver", `"$TEST_DRIVER_ACTIVE"`)
	// Windows does not expose POSIX executable mode bits consistently to Git's
	// shell, so this dynamic checksum/state test uses regular-file existence.
	helpers = strings.ReplaceAll(helpers, `[ -x "$FILE" ]`, `[ -f "$FILE" ]`)
	mock := `jq() {
  case "$2" in
    '.formatVersion // 1') printf 2 ;;
    '.targetVersion // empty') printf v2 ;;
    '.workerChecksum // empty') printf %s "$TEST_WORKER_CHECKSUM" ;;
    '.workerPreviousChecksum // empty') printf %s "$TEST_WORKER_PREVIOUS_CHECKSUM" ;;
    '.driverUpdated // false') printf true ;;
    '.driverHadPrevious // false') printf %s "$DRIVER_HAD_PREVIOUS" ;;
    '.driverChecksum // empty') printf %s "$TEST_DRIVER_CHECKSUM" ;;
    '.driverPreviousChecksum // empty') printf %s "$TEST_DRIVER_PREVIOUS_CHECKSUM" ;;
  esac
}
`
	checksum := func(value string) string {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	}
	for _, test := range []struct {
		name, worker, driver, want string
		driverHadPrevious          bool
	}{
		{name: "new pair", worker: "worker-new", driver: "driver-new", driverHadPrevious: true, want: "new"},
		{name: "old pair", worker: "worker-old", driver: "driver-old", driverHadPrevious: true, want: "old"},
		{name: "new Worker old driver", worker: "worker-new", driver: "driver-old", driverHadPrevious: true, want: "mixed"},
		{name: "old Worker new driver", worker: "worker-old", driver: "driver-new", driverHadPrevious: true, want: "mixed"},
		{name: "old Worker and absent former driver", worker: "worker-old", driverHadPrevious: false, want: "old"},
		{name: "old Worker and unexpected new driver", worker: "worker-old", driver: "driver-new", driverHadPrevious: false, want: "mixed"},
		{name: "unknown Worker", worker: "worker-damaged", driver: "driver-new", driverHadPrevious: true, want: "mixed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			workerActive := filepath.Join(dir, "agentbox-worker")
			driverActive := filepath.Join(dir, "agentbox-microsandbox-driver")
			marker := filepath.Join(dir, "marker.json")
			if err := os.WriteFile(workerActive, []byte(test.worker), 0o755); err != nil {
				t.Fatalf("write Worker: %v", err)
			}
			if test.driver != "" {
				if err := os.WriteFile(driverActive, []byte(test.driver), 0o755); err != nil {
					t.Fatalf("write driver: %v", err)
				}
			}
			if err := os.WriteFile(marker, []byte("marker"), 0o600); err != nil {
				t.Fatalf("write marker: %v", err)
			}
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(),
				"TEST_WORKER_ACTIVE="+filepath.ToSlash(workerActive),
				"TEST_DRIVER_ACTIVE="+filepath.ToSlash(driverActive),
				"TEST_WORKER_CHECKSUM="+checksum("worker-new"),
				"TEST_WORKER_PREVIOUS_CHECKSUM="+checksum("worker-old"),
				"TEST_DRIVER_CHECKSUM="+checksum("driver-new"),
				"TEST_DRIVER_PREVIOUS_CHECKSUM="+checksum("driver-old"),
				fmt.Sprintf("DRIVER_HAD_PREVIOUS=%t", test.driverHadPrevious),
				"MARKER="+filepath.ToSlash(marker),
			)
			command.Stdin = strings.NewReader("set -eu\n" + mock + helpers + `worker_update_pair_state "$MARKER"` + "\n")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("classify pair: %v: %s", err, output)
			}
			if got := string(output); got != test.want {
				t.Fatalf("pair state = %q, want %q", got, test.want)
			}
		})
	}
}

func TestScheduledWorkerRollbackRestoresPairBeforeRestart(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is not available on this test host")
	}
	const rollbackStart = "  cat > \"$ROLLBACK_SCRIPT\" <<'ROLLBACK'\n"
	start := strings.Index(workerDaemon, rollbackStart)
	if start < 0 {
		t.Fatal("scheduled Worker rollback script was not found")
	}
	start += len(rollbackStart)
	end := strings.Index(workerDaemon[start:], "\nROLLBACK\n")
	if end < 0 {
		t.Fatal("scheduled Worker rollback script boundary was not found")
	}
	rollback := workerDaemon[start : start+end]
	rollback = strings.Replace(rollback, "STATE_DIR=/var/lib/agentbox-worker", "STATE_DIR=$TEST_STATE_DIR", 1)
	rollback = strings.Replace(rollback, "CONFIG=/etc/agentbox-worker.conf", "CONFIG=$TEST_CONFIG", 1)
	rollback = strings.ReplaceAll(rollback, "/usr/local/bin/.agentbox-worker-restore", `"$TEST_WORKER_RESTORE_TMP"`)
	rollback = strings.ReplaceAll(rollback, "/usr/local/bin/.agentbox-microsandbox-driver-restore", `"$TEST_DRIVER_RESTORE_TMP"`)
	rollback = strings.ReplaceAll(rollback, "/usr/local/bin/agentbox-worker", `"$TEST_WORKER_ACTIVE"`)
	rollback = strings.ReplaceAll(rollback, "/usr/local/bin/agentbox-microsandbox-driver", `"$TEST_DRIVER_ACTIVE"`)
	rollback = strings.ReplaceAll(rollback, `[ -x "$1" ]`, `[ -f "$1" ]`)
	mock := `jq() {
  if [ "$1" != -r ]; then
    printf '{}\n'
    return
  fi
  case "$2" in
    '.jobId // empty') printf job-one ;;
    '.formatVersion // 1') printf 2 ;;
	'.leaseGeneration // 0') printf 1 ;;
    '.targetVersion // empty') printf v2 ;;
	'.workerPreviousVersion // empty') printf v1 ;;
    '.workerPrevious // empty') printf %s "$TEST_WORKER_PREVIOUS" ;;
    '.workerChecksum // empty') printf %s "$TEST_WORKER_CHECKSUM" ;;
    '.workerPreviousChecksum // empty') printf %s "$TEST_WORKER_PREVIOUS_CHECKSUM" ;;
    '.driverUpdated // false') printf true ;;
    '.driverHadPrevious // false') printf %s "$DRIVER_HAD_PREVIOUS" ;;
    '.driverPrevious // empty') printf %s "$TEST_DRIVER_PREVIOUS" ;;
    '.driverChecksum // empty') printf %s "$TEST_DRIVER_CHECKSUM" ;;
    '.driverPreviousChecksum // empty') printf %s "$TEST_DRIVER_PREVIOUS_CHECKSUM" ;;
  esac
}
install() {
  printf 'install:%s\n' "$3"
  [ "$FAIL_SOURCE" != "$3" ] || return 1
  command cp "$3" "$4"
  command chmod 0755 "$4"
}
mv() {
  [ "$1" != -f ] || shift
  printf 'move:%s\n' "$2"
  command mv "$1" "$2"
}
flock() { printf 'flock:%s\n' "$*"; }
systemctl() { printf 'systemctl:%s\n' "$*"; }
curl() { printf 204; }
`
	checksum := func(value string) string {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	}
	for _, test := range []struct {
		name              string
		fail              string
		driverHadPrevious bool
		corruptFallback   bool
		wantFailure       bool
	}{
		{name: "pair restored", driverHadPrevious: true},
		{name: "worker restore fails", fail: "worker", driverHadPrevious: true, wantFailure: true},
		{name: "driver restore fails", fail: "driver", driverHadPrevious: true, wantFailure: true},
		{name: "new driver removed", driverHadPrevious: false},
		{name: "corrupt fallback rejected before mutation", driverHadPrevious: true, corruptFallback: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			stateDir := filepath.Join(dir, "state")
			if err := os.Mkdir(stateDir, 0o755); err != nil {
				t.Fatalf("create state directory: %v", err)
			}
			workerActive := filepath.Join(dir, "agentbox-worker")
			workerPrevious := filepath.Join(dir, "worker.previous")
			driverActive := filepath.Join(dir, "agentbox-microsandbox-driver")
			driverPrevious := filepath.Join(dir, "driver.previous")
			config := filepath.Join(dir, "agentbox-worker.conf")
			marker := filepath.Join(stateDir, "worker-update.json")
			for path, content := range map[string]string{
				workerActive:   "worker-new",
				workerPrevious: "worker-old",
				driverActive:   "driver-new",
				driverPrevious: "driver-old",
				marker:         "marker",
			} {
				if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
					t.Fatalf("write %s: %v", filepath.Base(path), err)
				}
			}
			if err := os.WriteFile(config, []byte("http://agentbox.test\nserver-one\n"+strings.Repeat("c", 32)+"\n"), 0o600); err != nil {
				t.Fatalf("write Worker config: %v", err)
			}
			if test.corruptFallback {
				if err := os.WriteFile(driverPrevious, []byte("corrupt-driver"), 0o755); err != nil {
					t.Fatalf("corrupt driver fallback: %v", err)
				}
			}
			failSource := ""
			switch test.fail {
			case "worker":
				failSource = filepath.ToSlash(workerPrevious)
			case "driver":
				failSource = filepath.ToSlash(driverPrevious)
			}
			command := exec.CommandContext(t.Context(), sh, "-s", "--", filepath.ToSlash(marker))
			command.Env = append(command.Environ(),
				"TEST_STATE_DIR="+filepath.ToSlash(stateDir),
				"TEST_WORKER_ACTIVE="+filepath.ToSlash(workerActive),
				"TEST_WORKER_PREVIOUS="+filepath.ToSlash(workerPrevious),
				"TEST_WORKER_RESTORE_TMP="+filepath.ToSlash(filepath.Join(dir, "worker.restore")),
				"TEST_DRIVER_ACTIVE="+filepath.ToSlash(driverActive),
				"TEST_DRIVER_PREVIOUS="+filepath.ToSlash(driverPrevious),
				"TEST_DRIVER_RESTORE_TMP="+filepath.ToSlash(filepath.Join(dir, "driver.restore")),
				"TEST_WORKER_CHECKSUM="+checksum("worker-new"),
				"TEST_WORKER_PREVIOUS_CHECKSUM="+checksum("worker-old"),
				"TEST_DRIVER_CHECKSUM="+checksum("driver-new"),
				"TEST_DRIVER_PREVIOUS_CHECKSUM="+checksum("driver-old"),
				"TEST_CONFIG="+filepath.ToSlash(config),
				fmt.Sprintf("DRIVER_HAD_PREVIOUS=%t", test.driverHadPrevious),
				"FAIL_SOURCE="+failSource,
			)
			command.Stdin = strings.NewReader(mock + rollback + "\n")
			output, err := command.CombinedOutput()
			if test.wantFailure == (err == nil) {
				t.Fatalf("scheduled rollback = (%q, %v), want failure %t", output, err, test.wantFailure)
			}
			outputText := string(output)
			if test.corruptFallback {
				if strings.Contains(outputText, "install:") {
					t.Fatalf("corrupt pair was mutated before full fallback validation: %q", output)
				}
				return
			}
			if !strings.Contains(outputText, "install:"+filepath.ToSlash(workerPrevious)) {
				t.Fatalf("scheduled rollback did not attempt Worker restore: %q", output)
			}
			if test.driverHadPrevious && !strings.Contains(outputText, "install:"+filepath.ToSlash(driverPrevious)) {
				t.Fatalf("scheduled rollback did not attempt driver restore: %q", output)
			}
			if !test.wantFailure {
				unlock := strings.Index(outputText, "flock:-u 9")
				restart := strings.Index(outputText, "systemctl:start agentbox-worker.service")
				if unlock < 0 || restart < 0 || unlock >= restart {
					t.Fatalf("successful restore must release the shared lock before restart: %q", output)
				}
			} else if strings.Contains(outputText, "systemctl:start agentbox-worker.service") {
				t.Fatalf("partial pair restore restarted Worker: %q", output)
			}
		})
	}
}

func TestBackupRestoreGuideUsesQuiescedAtomicRecovery(t *testing.T) {
	readmeBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	_, guide, found := strings.Cut(string(readmeBytes), "## 备份与恢复")
	if !found {
		t.Fatal("README backup and restore guide was not found")
	}
	guide, _, _ = strings.Cut(guide, "## 核心能力")
	for _, expected := range []string{
		`set -eu`,
		`backup_name=agentbox-backup-$(date -u +%Y%m%dT%H%M%SZ)`,
		`backup_tmp=$(mktemp -d "./.${backup_name}.tmp.XXXXXX")`,
		`sha256sum database.sql agentbox-secrets.tar.gz > SHA256SUMS`,
		`mv "$backup_tmp" "$backup_final"`,
		`backup_dir=./agentbox-backup-20260903T120000Z`,
		`sha256sum -c SHA256SUMS`,
		`docker compose stop app server`,
		`pg_dump --format=plain --no-owner --no-privileges`,
		`tar tzf /backup/agentbox-secrets.tar.gz`,
		`/*|..|../*|*/../*|*/..)`,
		`secret-key must be 43 raw or 44 padded base64 characters`,
		`printf %s "$padded" | base64 -d`,
		`test "$(wc -c < "$staging/decoded-key" | tr -d " ")" = 32`,
		`find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} +`,
		`cp -a "$staging/secret-key" /data/secret-key`,
		`test -s /data/secret-key`,
		`psql -v ON_ERROR_STOP=1`,
		`--single-transaction`,
		`DROP DATABASE IF EXISTS agentbox`,
		`docker compose up -d --wait server app`,
		`http://127.0.0.1:8091/healthz`,
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("safe backup/restore guide is missing %q", expected)
		}
	}
	backupKeyValidation := strings.Index(guide, `test "$(wc -c < "$staging/decoded-key" | tr -d " ")" = 32`)
	manifest := strings.Index(guide, `sha256sum database.sql agentbox-secrets.tar.gz > SHA256SUMS`)
	publish := strings.Index(guide, `mv "$backup_tmp" "$backup_final"`)
	if backupKeyValidation < 0 || manifest < 0 || publish < 0 ||
		backupKeyValidation >= manifest || manifest >= publish {
		t.Fatal("backup guide must validate the key and bind both artifacts before its atomic publish point")
	}
	restoreStart := strings.Index(guide, "恢复会替换目标实例")
	if restoreStart < 0 {
		t.Fatal("restore guide was not found")
	}
	restoreGuide := guide[restoreStart:]
	databaseInputCheck := strings.Index(restoreGuide, `test -s "$backup_dir/database.sql"`)
	archiveCheck := strings.Index(restoreGuide, `tar tzf /backup/agentbox-secrets.tar.gz > "$staging/archive.list"`)
	keyValidation := strings.Index(restoreGuide, `test "$(wc -c < "$staging/decoded-key" | tr -d " ")" = 32`)
	clearSecretVolume := strings.Index(restoreGuide, `find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} +`)
	databaseRestore := strings.Index(restoreGuide, `--single-transaction`)
	restart := strings.Index(restoreGuide, `docker compose up -d --wait server app`)
	if databaseInputCheck < 0 || archiveCheck < 0 || keyValidation < 0 || clearSecretVolume < 0 ||
		databaseRestore < 0 || restart < 0 || databaseInputCheck >= archiveCheck ||
		archiveCheck >= keyValidation || keyValidation >= clearSecretVolume ||
		clearSecretVolume >= databaseRestore || databaseRestore >= restart {
		t.Fatal("restore guide must validate both inputs and the complete key before destructive recovery or writer startup")
	}
}

func TestWorkerInstallerIncludesPinnedMicroVMRuntimeSDKs(t *testing.T) {
	for _, expected := range []string{
		`BOXLITE_VERSION=0.9.7`,
		`boxlite-cli-v${BOXLITE_VERSION}-${BOXLITE_ARCH}-unknown-linux-gnu.tar.gz`,
		`sha256sum -c`,
		`/usr/local/bin/boxlite`,
		`/api/worker/agentbox-microsandbox-driver.go`,
		`github.com/superradcompany/microsandbox/sdk/go v0.6.15`,
		`GOPROXY="${AGENTBOX_GOPROXY:-https://goproxy.cn,direct}" go mod tidy`,
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
		`skopeo copy --insecure-policy "docker-daemon:$IMAGE" "oci:$TEMP_ROOTFS:agentbox"`,
		`agentbox-worker image-to-oci \
       --output "$ROOTFS_PATH" --reference "$IMAGE"`,
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
	if strings.Contains(workerDaemon, `command docker image save`) ||
		strings.Contains(workerDaemon, `ARCHIVE_PATH=$(mktemp "$STATE_DIR/docker-image.`) {
		t.Fatal("BoxLite Docker image conversion must not materialize a full temporary archive")
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
		`boxlite_delete()`,
		`HTTP_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' "$BOXLITE_URL/v1/boxes/$TARGET")`,
		`404) return 0 ;;`,
		`boxlite_cli rm --force "$TARGET"`,
		`BoxLite did not remove $TARGET`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite idempotent delete is missing %q", expected)
		}
	}
}

func TestWorkerBoxLiteReplacesFailedBoxBeforeCreate(t *testing.T) {
	createStart := strings.Index(workerDaemon, "    create)\n")
	createEnd := strings.Index(workerDaemon[createStart:], "    inspect)")
	if createStart < 0 || createEnd < 0 {
		t.Fatal("BoxLite create action was not found")
	}
	createBody := workerDaemon[createStart : createStart+createEnd]
	for _, expected := range []string{
		`BOX_STATUS=$(printf '%s' "$BOX_JSON" | boxlite_inspect_status)`,
		`failed|error)`,
		`boxlite_delete "$TARGET"`,
		`if [ "$BOX_EXISTS" = false ]; then`,
	} {
		if !strings.Contains(createBody, expected) {
			t.Fatalf("BoxLite failed-box replacement is missing %q", expected)
		}
	}
	deleteIndex := strings.Index(createBody, `boxlite_delete "$TARGET"`)
	createIndex := strings.Index(createBody, `boxlite_create_with_rootfs "$TARGET"`)
	if deleteIndex < 0 || createIndex < 0 || deleteIndex >= createIndex {
		t.Fatal("BoxLite must delete a failed box before creating its replacement")
	}

	for _, expected := range []string{
		`boxlite_inspect_status()`,
		`(.[0].Status // .[0].status // .[0].State.Status // .[0].state.status // "")`,
		`(.Status // .status // .State.Status // .state.status // "")`,
		`boxlite_inspect_usable()`,
		`failed|error) return 1 ;;`,
		`inspect) boxlite_inspect_usable "$1" ;;`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("BoxLite failed-box inspection is missing %q", expected)
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

func TestWorkerInjectsProxyBeforeAgentInstallationWithoutPersistingSecret(t *testing.T) {
	for _, expected := range []string{
		`configure_proxy()`,
		`/opt/agentbox/secrets/proxy.env`,
		`[ ! -r /opt/agentbox/secrets/proxy.env ] || . /opt/agentbox/secrets/proxy.env`,
		`append_env "$PROXY_ENV" NODE_USE_ENV_PROXY 1`,
		`append_env "$PROXY_ENV" CLAUDE_CODE_PROXY_RESOLVES_HOSTS 1`,
		`HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy`,
		`"$(sandbox_control_plane_host "$DRIVER")" host.boxlite.internal`,
		`del(.credentials, .proxy)`,
		`.job.payload.controlPlane.allowNet[]?, .job.payload.proxy.allowNet[]?`,
		`--allow-net "$ALLOWED_HOST"`,
		`network: {mode: $network, allow_net: $allowNet}`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker proxy integration is missing %q", expected)
		}
	}

	prepareStart := strings.Index(workerDaemon, "prepare_agent_image()")
	prepareEnd := strings.Index(workerDaemon[prepareStart:], "remove_proxy()")
	if prepareStart < 0 || prepareEnd < 0 {
		t.Fatal("Worker cached-image preparation function was not found")
	}
	prepareBody := workerDaemon[prepareStart : prepareStart+prepareEnd]
	configureIndex := strings.Index(prepareBody, `configure_proxy "$BUILD_CONTAINER" "$JOB_FILE"`)
	installIndex := strings.Index(prepareBody, `install_agent_tools "$BUILD_CONTAINER" "$JOB_FILE"`)
	if configureIndex < 0 || installIndex < 0 || configureIndex >= installIndex {
		t.Fatal("Worker must inject proxy settings before installing Agent tools")
	}

	configureStart := strings.Index(workerDaemon, "configure_proxy()")
	if configureStart < 0 {
		t.Fatal("Worker proxy configuration function was not found")
	}
	configureEnd := strings.Index(workerDaemon[configureStart:], "\n}\n")
	if configureEnd < 0 {
		t.Fatal("Worker proxy configuration function was not found")
	}
	configureBody := workerDaemon[configureStart : configureStart+configureEnd]
	setExportIndex := strings.Index(configureBody, `'  set -a'`)
	sourceIndex := strings.Index(configureBody, `'  . /opt/agentbox/secrets/proxy.env'`)
	clearExportIndex := strings.Index(configureBody, `'  set +a'`)
	if setExportIndex < 0 || sourceIndex <= setExportIndex || clearExportIndex <= sourceIndex {
		t.Fatal("Worker must export proxy variables while loading the proxy environment")
	}
}

func TestWorkerNetworkPolicyDefaultsAreFailClosed(t *testing.T) {
	for _, expected := range []string{
		`effective_sandbox_network_policy()`,
		`NETWORK=$(effective_sandbox_network_policy "$DRIVER" "$(jq -r '.job.payload.network // empty' "$JOB_FILE")")`,
		`printf none`,
		`restricted network is only supported by BoxLite`,
		`unsupported network policy: $1`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker network policy guard is missing %q", expected)
		}
	}
}

func TestWorkerChecksDockerNetworkModeBeforeReusingContainer(t *testing.T) {
	for _, expected := range []string{
		`enforce_docker_network_policy()`,
		`docker inspect -f '{{.HostConfig.NetworkMode}}|{{range $name, $config := .NetworkSettings.Networks}}`,
		`none:none:none,`,
		`egress:default:bridge,`,
		`docker stop --time 10 "$TARGET"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker Docker network drift guard is missing %q", expected)
		}
	}
	createStart := strings.Index(workerDaemon, "create_sandbox()")
	startStart := strings.Index(workerDaemon, "start_sandbox()")
	restartStart := strings.Index(workerDaemon, "restart_sandbox()")
	applyProxyStart := strings.Index(workerDaemon, "apply_sandbox_proxy()")
	if createStart < 0 || startStart <= createStart || restartStart <= startStart || applyProxyStart <= restartStart {
		t.Fatal("Worker sandbox lifecycle boundaries are missing")
	}
	createBody := workerDaemon[createStart:startStart]
	startBody := workerDaemon[startStart:restartStart]
	restartBody := workerDaemon[restartStart:applyProxyStart]
	for name, body := range map[string]string{"create": createBody, "start": startBody} {
		guardIndex := strings.Index(body, `enforce_docker_network_policy "$TARGET" "$NETWORK"`)
		startIndex := strings.Index(body, `docker start "$TARGET"`)
		if guardIndex < 0 || startIndex < 0 || guardIndex >= startIndex {
			t.Fatalf("%s path does not validate Docker network mode before start", name)
		}
	}
	stopIndex := strings.Index(restartBody, `stop_sandbox "$JOB_FILE"`)
	startIndex := strings.Index(restartBody, `start_sandbox "$JOB_FILE"`)
	if stopIndex < 0 || startIndex < 0 || stopIndex >= startIndex {
		t.Fatal("restart path must stop the legacy container before the guarded start")
	}
}

func TestWorkerOwnsWorkspaceVolumesAndRemovesAnonymousVolumes(t *testing.T) {
	for _, expected := range []string{
		`docker volume create --label "agentbox.sandbox=$SANDBOX_ID" "$VOLUME"`,
		`docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$VOLUME"`,
		`docker rm -f -v "$TARGET"`,
		`docker rm -f -v "$BUILD_CONTAINER"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker Docker lifecycle is missing %q", expected)
		}
	}
}

func TestWorkerNormalizesEmptyNetworkPolicyBeforeValidation(t *testing.T) {
	for _, expected := range []string{
		`effective_sandbox_network_policy()`,
		`elif [ "$1" = boxlite ]; then`,
		`printf none`,
		`NETWORK=$(effective_sandbox_network_policy "$DRIVER"`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker network policy normalization is missing %q", expected)
		}
	}
}

func TestWorkerAppliesSandboxProxyWithoutRecreatingSandbox(t *testing.T) {
	for _, expected := range []string{
		`apply_sandbox_proxy()`,
		`configure-sandbox-proxy)`,
		`configure_proxy "$TARGET" "$JOB_FILE"`,
		`docker exec "$TARGET" test -s /opt/agentbox/secrets/proxy.env`,
		`docker exec "$TARGET" test -e /opt/agentbox/secrets/proxy.env`,
		`sandbox_proxy_apply_failed proxy-config true`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker live proxy update is missing %q", expected)
		}
	}
}

func TestWorkerNetworkProxyCheckRunsOnWorkerWithoutCommandLineSecret(t *testing.T) {
	for _, expected := range []string{
		`check_network_proxy()`,
		`check-network-proxy)`,
		`HTTP_PROXY="$PROXY_URL" HTTPS_PROXY="$PROXY_URL" ALL_PROXY="$PROXY_URL"`,
		`NO_PROXY='' no_proxy=''`,
		`--connect-timeout 5 --max-time 10 "$TARGET"`,
		`network_proxy_check_failed proxy-check true`,
	} {
		if !strings.Contains(workerDaemon, expected) {
			t.Fatalf("Worker proxy check is missing %q", expected)
		}
	}
	if strings.Contains(workerDaemon, `--proxy "$PROXY_URL"`) {
		t.Fatal("Worker proxy check must not expose proxy credentials in curl command arguments")
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
