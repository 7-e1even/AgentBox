package workerscript

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestSandboxLifecyclePropagatesFailuresInsideWorkerConditional(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, lifecycle, found := strings.Cut(workerDaemon, "\nstart_sandbox() {")
	if !found {
		t.Fatal("Worker start function is missing")
	}
	lifecycle, _, found = strings.Cut(lifecycle, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker lifecycle function boundary is missing")
	}
	lifecycle = "start_sandbox() {" + lifecycle
	for _, test := range []struct {
		name, driver, operation, failure string
	}{
		{"start success", "docker", "start", ""},
		{"restart success", "docker", "restart", ""},
		{"docker start", "docker", "start", "docker-start"},
		{"docker network policy", "docker", "start", "docker-network"},
		{"boxlite start", "boxlite", "start", "boxlite-start"},
		{"boxlite guest", "boxlite", "start", "boxlite-guest"},
		{"microsandbox workdir", "microsandbox", "start", "microsandbox-fs-mkdir"},
		{"agent installation", "docker", "start", "agent-tools"},
		{"variable resolution", "docker", "start", "variables-resolve"},
		{"credential injection", "docker", "start", "credentials"},
		{"variable injection", "docker", "start", "variables"},
		{"skill injection", "docker", "start", "skills"},
		{"MCP injection", "docker", "start", "mcp"},
		{"wrapper installation", "docker", "start", "wrappers"},
		{"restart Docker stop", "docker", "restart", "docker-stop"},
		{"restart BoxLite stop", "boxlite", "restart", "boxlite-stop"},
		{"restart configuration", "docker", "restart", "credentials"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Run the real functions under the same if/command-substitution shape
			// as process_job: set -e alone does not propagate failures here.
			mock := `check_step() { [ "$FAIL_STEP" != "$1" ]; }
jq() {
  case "$*" in
    *payload.driver*) printf '%s' "$TEST_DRIVER" ;;
    *payload.externalId*) printf example-container ;;
    *payload.workdir*) printf /workspace ;;
    *) return 0 ;;
  esac
}
sandbox_container_name() { printf example-container; }
validate_sandbox_network_policy_value() { return 0; }
validate_sandbox_network_policy() { return 0; }
effective_sandbox_network_policy() { printf egress; }
enforce_docker_network_policy() { check_step docker-network; }
docker() { case "$1" in start|stop) check_step "docker-$1" ;; *) return 0 ;; esac; }
runtime_call() { check_step "$1-$2"; }
install_boxlite_guest() { check_step boxlite-guest; }
report_job_progress() { return 0; }
configure_proxy() { return 0; }
install_agent_tools() { check_step agent-tools; }
resolve_worker_variables() { check_step variables-resolve; }
configure_credentials() { check_step credentials; }
configure_variables() { check_step variables; }
configure_skills() { check_step skills; }
configure_mcp_servers() { check_step mcp; }
install_agent_wrappers() { check_step wrappers; }
configure_sandbox_agent_config() {
  resolve_worker_variables || return 1
  configure_credentials || return 1
  configure_variables || return 1
  configure_skills || return 1
  configure_mcp_servers || return 1
  install_agent_wrappers || return 1
}
ensure_desktop() { return 0; }
`
			invoke := `
if result=$(${TEST_OPERATION}_sandbox ignored); then
  printf 'success:%s' "$result"
else
  exit 1
fi
`
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_DRIVER="+test.driver,
				"TEST_OPERATION="+test.operation, "FAIL_STEP="+test.failure)
			command.Stdin = strings.NewReader("set -eu\n" + mock + lifecycle + invoke)
			output, err := command.CombinedOutput()
			if test.failure == "" {
				if err != nil || string(output) != "success:example-container" {
					t.Fatalf("successful operation = (%q, %v)", output, err)
				}
			} else if err == nil || strings.Contains(string(output), "success:") {
				t.Fatalf("failure %s was reported as success: (%q, %v)", test.failure, output, err)
			}
		})
	}
}

func TestSandboxSetupOutputCannotEnterCreateFailureLog(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	create := daemonSection(t, "create_sandbox() {", "sandbox_container_name() {")
	root := t.TempDir()
	logPath := root + "/create.log"
	outputPath := root + "/create.output"
	const marker = "setup-output-must-not-leak"
	const secret = "setup-secret-must-not-leak"
	mock := `
jq() {
  case "$*" in
    *payload.extensions*) return 1 ;;
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf docker ;;
    *payload.image*) printf runtime:latest ;;
    *payload.workdir*) printf /workspace ;;
    *payload.cpu*|*payload.memory*) printf '' ;;
    *payload.network*) printf egress ;;
    *payload.setup*) printf '%s' "$SETUP_COMMAND" ;;
    *payload.desktop*) return 1 ;;
    *'type == "object"'*) return 0 ;;
    *) return 1 ;;
  esac
}
timeout() { shift; "$@"; }
docker() {
  case "$*" in
    info) return 0 ;;
    'inspect agentbox-sandbox-one') return 0 ;;
    *'.Config.Labels "agentbox.sandbox"'*) printf sandbox-one ;;
    'start agentbox-sandbox-one') return 0 ;;
    *"sh -lc $SETUP_COMMAND"*) printf '%s\n' "$SETUP_MARKER"; printf '%s\n' "$SETUP_SECRET" >&2; return 7 ;;
    *) return 0 ;;
  esac
}
effective_sandbox_network_policy() { printf egress; }
validate_sandbox_network_policy_value() { :; }
validate_sandbox_network_policy() { :; }
enforce_docker_network_policy() { :; }
record_create_resource() { :; }
report_job_progress() { :; }
configure_proxy() { :; }
build_sandbox_manifest() { printf '{}\n' > "$2"; }
write_container_file_atomic() { :; }
resolve_worker_variables() { printf '{}\n' > "$2"; }
configure_sandbox_agent_config() { :; }
install_sandbox_extensions() { :; }
ensure_desktop() { :; }
`
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Env = append(command.Environ(), "TEST_LOG="+shellPath(logPath), "TEST_OUTPUT="+shellPath(outputPath),
		"SETUP_COMMAND=fixture-setup", "SETUP_MARKER="+marker, "SETUP_SECRET="+secret)
	command.Stdin = strings.NewReader("set -eu\n" + mock + create + `
if create_sandbox ignored >"$TEST_OUTPUT" 2>"$TEST_LOG"; then exit 40; fi
cat "$TEST_LOG"
`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("exercise failing setup: %v\n%s", err, output)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	completionMessage := string(log)
	if strings.Contains(completionMessage, marker) || strings.Contains(completionMessage, secret) ||
		!strings.Contains(completionMessage, "stage setup-command failed") {
		t.Fatalf("create failure log = %q", completionMessage)
	}
}

func TestDockerSandboxReuseEnforcesNetworkMode(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, guard, found := strings.Cut(workerDaemon, "\nenforce_docker_network_policy() {")
	if !found {
		t.Fatal("Worker Docker network policy guard is missing")
	}
	guard, _, found = strings.Cut(guard, "\ncreate_sandbox() {")
	if !found {
		t.Fatal("Worker Docker network policy guard boundary is missing")
	}
	guard = "enforce_docker_network_policy() {" + guard
	mock := `docker() {
  case "$1:${2:-}" in
    inspect:-f)
      [ "$ACTUAL_NETWORK" != inspect-error ] || return 1
      printf '%s|%s' "$ACTUAL_NETWORK" "$ACTUAL_NETWORKS"
      ;;
    stop:--time) STOPPED=true ;;
    *) return 2 ;;
  esac
}
STOPPED=false
`
	for _, test := range []struct {
		name, actual, networks, expected, wantResult string
	}{
		{name: "none matches none", actual: "none", networks: "none,", expected: "none", wantResult: "allowed:false"},
		{name: "empty attachments match none", actual: "none", expected: "none", wantResult: "allowed:false"},
		{name: "attached bridge cannot bypass none", actual: "none", networks: "bridge,none,", expected: "none", wantResult: "denied:true"},
		{name: "default bridge matches egress", actual: "default", networks: "bridge,", expected: "egress", wantResult: "allowed:false"},
		{name: "named bridge mode is rejected", actual: "private", networks: "private,", expected: "egress", wantResult: "denied:true"},
		{name: "extra network cannot bypass egress", actual: "default", networks: "bridge,private,", expected: "egress", wantResult: "denied:true"},
		{name: "legacy bridge cannot satisfy none", actual: "bridge", networks: "bridge,", expected: "none", wantResult: "denied:true"},
		{name: "none is safe while restricted is rejected later", actual: "none", networks: "none,", expected: "restricted", wantResult: "allowed:false"},
		{name: "legacy bridge cannot satisfy restricted", actual: "bridge", networks: "bridge,", expected: "restricted", wantResult: "denied:true"},
		{name: "uninspectable container is stopped", actual: "inspect-error", expected: "none", wantResult: "denied:true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "ACTUAL_NETWORK="+test.actual, "ACTUAL_NETWORKS="+test.networks, "EXPECTED_NETWORK="+test.expected)
			command.Stdin = strings.NewReader("set -eu\n" + mock + guard + `
if enforce_docker_network_policy example-container "$EXPECTED_NETWORK"; then
  RESULT=allowed
else
  RESULT=denied
fi
printf '%s:%s' "$RESULT" "$STOPPED"
`)
			output, err := command.CombinedOutput()
			if err != nil || !strings.HasSuffix(string(output), test.wantResult) {
				t.Fatalf("network mode %s for %s = (%q, %v), want suffix %q", test.actual, test.expected, output, err, test.wantResult)
			}
			if strings.HasPrefix(test.wantResult, "denied") && !strings.Contains(string(output), "stage network-policy failed") {
				t.Fatalf("network mismatch omitted failure reason: %q", output)
			}
		})
	}
}

func TestSandboxCreationRejectsRestrictedNetworkOutsideBoxLite(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, create, found := strings.Cut(workerDaemon, "\nvalidate_sandbox_network_policy_value() {")
	if !found {
		t.Fatal("Worker network policy guard is missing")
	}
	create, _, found = strings.Cut(create, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker guarded lifecycle boundary is missing")
	}
	create = "validate_sandbox_network_policy_value() {" + create
	mock := `jq() {
  case "$*" in
    *payload.extensions*) return 1 ;;
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf '%s' "$TEST_DRIVER" ;;
    *payload.network*) printf restricted ;;
    *) printf '' ;;
  esac
}
report_job_progress() { :; }
timeout() { shift; "$@"; }
docker() {
  printf '%s\n' "$*" >> "$DOCKER_CALLS"
  case "$*" in
    info) return 0 ;;
    'inspect agentbox-sandbox-one') return 0 ;;
    *'.Config.Labels'* ) printf sandbox-one ;;
    *'.HostConfig.NetworkMode'* ) printf 'bridge|bridge,' ;;
    'stop agentbox-sandbox-one') return 0 ;;
    'stop --time 10 agentbox-sandbox-one') return 0 ;;
    *) return 2 ;;
  esac
}
`
	for _, driver := range []string{"docker", "microsandbox"} {
		for _, operation := range []string{"create", "start", "restart"} {
			t.Run(driver+" "+operation, func(t *testing.T) {
				dockerCalls := t.TempDir() + "/docker-calls"
				command := exec.CommandContext(t.Context(), sh, "-s")
				command.Env = append(command.Environ(), "TEST_DRIVER="+driver, "TEST_OPERATION="+operation, "DOCKER_CALLS="+dockerCalls)
				command.Stdin = strings.NewReader("set -eu\n" + mock + create + "\n${TEST_OPERATION}_sandbox ignored\n")
				output, err := command.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "restricted network is only supported by BoxLite") {
					t.Fatalf("restricted %s network was not rejected for %s: (%q, %v)", driver, operation, output, err)
				}
				calls, readErr := os.ReadFile(dockerCalls)
				if readErr != nil && !os.IsNotExist(readErr) {
					t.Fatalf("read Docker calls: %v", readErr)
				}
				if driver == "docker" && !strings.Contains(string(calls), "stop --time 10 agentbox-sandbox-one") {
					t.Fatalf("restricted %s did not stop the legacy Docker container; calls = %q", operation, calls)
				}
			})
		}
	}
}

func TestInvalidDockerNetworkPolicyDoesNotStopSandbox(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, lifecycle, found := strings.Cut(workerDaemon, "\nvalidate_sandbox_network_policy_value() {")
	if !found {
		t.Fatal("Worker network policy value guard is missing")
	}
	lifecycle, _, found = strings.Cut(lifecycle, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker guarded lifecycle boundary is missing")
	}
	lifecycle = "validate_sandbox_network_policy_value() {" + lifecycle
	mock := `jq() {
  case "$*" in
    *payload.extensions*) return 1 ;;
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf docker ;;
    *payload.network*) printf corrupt-policy ;;
    *) printf '' ;;
  esac
}
docker() { printf '%s\n' "$*" >> "$DOCKER_CALLS"; return 0; }
`
	for _, operation := range []string{"create", "start", "restart"} {
		t.Run(operation, func(t *testing.T) {
			dockerCalls := t.TempDir() + "/docker-calls"
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_OPERATION="+operation, "DOCKER_CALLS="+dockerCalls)
			command.Stdin = strings.NewReader("set -eu\n" + mock + lifecycle + "\n${TEST_OPERATION}_sandbox ignored\n")
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "unsupported network policy: corrupt-policy") {
				t.Fatalf("invalid policy was not rejected for %s: (%q, %v)", operation, output, err)
			}
			calls, readErr := os.ReadFile(dockerCalls)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatalf("read Docker calls: %v", readErr)
			}
			if len(calls) != 0 {
				t.Fatalf("invalid %s policy touched Docker: %q", operation, calls)
			}
		})
	}
}

func TestDockerSandboxDeleteIsIdempotentAndPropagatesFailures(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, remove, found := strings.Cut(workerDaemon, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker delete function is missing")
	}
	remove, _, found = strings.Cut(remove, "\ncheck_network_proxy() {")
	if !found {
		t.Fatal("Worker delete function boundary is missing")
	}
	remove = "delete_sandbox() {" + remove
	mock := `jq() {
  case "$*" in
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf docker ;;
  esac
}
sandbox_container_name() { printf agentbox-sandbox-one; }
timeout() { shift; "$@"; }
check_step() { [ "$FAIL_STEP" != "$1" ]; }
docker() {
  case "$1:${2:-}" in
    info:) check_step info ;;
    container:ls)
      check_step container-list || return 1
      if [ "$CONTAINER_PRESENT" = true ]; then printf agentbox-sandbox-one; fi
      ;;
    inspect:-f)
      check_step inspect || return 1
      case "$3" in
        *Config.Labels*) printf '%s' "$OWNER" ;;
        *Mounts*) if [ "$MOUNTED_VOLUME" = true ]; then printf agentbox-sandbox-one-workspace; fi ;;
      esac
      ;;
    rm:-f) check_step container-remove ;;
    volume:ls)
      check_step volume-list || return 1
      if [ "$VOLUME_PRESENT" = true ]; then printf agentbox-sandbox-one-workspace; fi
      ;;
    volume:inspect)
      check_step volume-inspect || return 1
      printf '%s' "$VOLUME_OWNER"
      ;;
    volume:rm) check_step volume-remove ;;
    *) return 2 ;;
  esac
}
`
	for _, test := range []struct {
		name             string
		containerPresent bool
		volumePresent    bool
		owner            string
		volumeOwner      string
		mountedVolume    bool
		failure          string
		wantSuccess      bool
	}{
		{name: "missing resources", owner: "sandbox-one", wantSuccess: true},
		{name: "delete existing resources", containerPresent: true, volumePresent: true, owner: "sandbox-one", mountedVolume: true, wantSuccess: true},
		{name: "delete labeled orphan volume", volumePresent: true, owner: "sandbox-one", volumeOwner: "sandbox-one", wantSuccess: true},
		{name: "leave unverifiable legacy orphan volume", volumePresent: true, owner: "sandbox-one", wantSuccess: true},
		{name: "daemon unavailable", owner: "sandbox-one", failure: "info"},
		{name: "container inspection fails", owner: "sandbox-one", failure: "container-list"},
		{name: "ownership mismatch", containerPresent: true, owner: "another-sandbox"},
		{name: "container removal fails", containerPresent: true, owner: "sandbox-one", failure: "container-remove"},
		{name: "volume inspection fails", owner: "sandbox-one", failure: "volume-list"},
		{name: "volume ownership mismatch", volumePresent: true, owner: "sandbox-one", volumeOwner: "another-sandbox"},
		{name: "mounted volume ownership mismatch", containerPresent: true, volumePresent: true, owner: "sandbox-one", volumeOwner: "another-sandbox", mountedVolume: true},
		{name: "volume ownership inspection fails", volumePresent: true, owner: "sandbox-one", failure: "volume-inspect"},
		{name: "volume removal fails", volumePresent: true, owner: "sandbox-one", volumeOwner: "sandbox-one", failure: "volume-remove"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(),
				"CONTAINER_PRESENT="+strconv.FormatBool(test.containerPresent),
				"VOLUME_PRESENT="+strconv.FormatBool(test.volumePresent),
				"OWNER="+test.owner,
				"VOLUME_OWNER="+test.volumeOwner,
				"MOUNTED_VOLUME="+strconv.FormatBool(test.mountedVolume),
				"FAIL_STEP="+test.failure,
			)
			command.Stdin = strings.NewReader("set -eu\nSTATE_DIR=$(mktemp -d)\ntrap 'rm -rf \"$STATE_DIR\"' EXIT\n" + mock + remove + "\ndelete_sandbox ignored\n")
			output, err := command.CombinedOutput()
			if test.wantSuccess && err != nil {
				t.Fatalf("idempotent delete failed: (%q, %v)", output, err)
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("delete failure %q was reported as success: %q", test.failure, output)
			}
		})
	}
}

func TestDockerSandboxDeleteRetriesLegacyVolumeWithPersistedOwnership(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, remove, found := strings.Cut(workerDaemon, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker delete function is missing")
	}
	remove, _, found = strings.Cut(remove, "\ncheck_network_proxy() {")
	if !found {
		t.Fatal("Worker delete function boundary is missing")
	}
	remove = "delete_sandbox() {" + remove
	script := `set -eu
STATE_DIR=$(mktemp -d)
trap 'rm -rf "$STATE_DIR"' EXIT
jq() {
  case "$*" in
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf docker ;;
  esac
}
sandbox_container_name() { printf agentbox-sandbox-one; }
timeout() { shift; "$@"; }
docker() {
  case "$1:${2:-}" in
    info:) return 0 ;;
    container:ls) [ -f "$STATE_DIR/container-removed" ] || printf agentbox-sandbox-one ;;
    inspect:-f)
      case "$3" in
        *Config.Labels*) printf sandbox-one ;;
        *Mounts*) printf agentbox-sandbox-one-workspace ;;
      esac
      ;;
    rm:-f) : > "$STATE_DIR/container-removed" ;;
    volume:ls) [ -f "$STATE_DIR/volume-removed" ] || printf agentbox-sandbox-one-workspace ;;
    volume:inspect)
      case "$4" in
        *Labels*) printf '' ;;
        *Mountpoint*) printf '/var/lib/docker/volumes/agentbox-sandbox-one-workspace|2026-09-03T00:00:00Z' ;;
      esac
      ;;
    volume:rm)
      if [ ! -f "$STATE_DIR/remove-failed-once" ]; then
        : > "$STATE_DIR/remove-failed-once"
        return 1
      fi
      : > "$STATE_DIR/volume-removed"
      ;;
    *) return 2 ;;
  esac
}
`
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Stdin = strings.NewReader(script + remove + `
if delete_sandbox ignored; then exit 40; fi
delete_sandbox ignored
test -f "$STATE_DIR/volume-removed"
test ! -e "$STATE_DIR/docker-volume-ownership/sandbox-one"
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("retrying legacy volume delete failed: (%q, %v)", output, err)
	}
}
