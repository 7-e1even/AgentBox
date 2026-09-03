package workerscript

import (
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
		{"boxlite start", "boxlite", "start", "boxlite-start"},
		{"boxlite guest", "boxlite", "start", "boxlite-guest"},
		{"microsandbox workdir", "microsandbox", "start", "microsandbox-fs-mkdir"},
		{"agent installation", "docker", "start", "agent-tools"},
		{"credential injection", "docker", "start", "credentials"},
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
validate_sandbox_network_policy() { return 0; }
effective_sandbox_network_policy() { printf egress; }
docker() { case "$1" in start|stop) check_step "docker-$1" ;; *) return 0 ;; esac; }
runtime_call() { check_step "$1-$2"; }
install_boxlite_guest() { check_step boxlite-guest; }
report_job_progress() { return 0; }
configure_proxy() { return 0; }
install_agent_tools() { check_step agent-tools; }
configure_credentials() { check_step credentials; }
configure_skills() { check_step skills; }
configure_mcp_servers() { check_step mcp; }
install_agent_wrappers() { check_step wrappers; }
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

func TestSandboxCreationRejectsRestrictedNetworkOutsideBoxLite(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	_, create, found := strings.Cut(workerDaemon, "\nvalidate_sandbox_network_policy() {")
	if !found {
		t.Fatal("Worker network policy guard is missing")
	}
	create, _, found = strings.Cut(create, "\ndelete_sandbox() {")
	if !found {
		t.Fatal("Worker guarded lifecycle boundary is missing")
	}
	create = "validate_sandbox_network_policy() {" + create
	mock := `jq() {
  case "$*" in
    *payload.extensions*) return 1 ;;
    *payload.sandboxId*) printf sandbox-one ;;
    *payload.driver*) printf '%s' "$TEST_DRIVER" ;;
    *payload.network*) printf restricted ;;
    *) printf '' ;;
  esac
}
`
	for _, driver := range []string{"docker", "microsandbox"} {
		for _, operation := range []string{"create", "start", "restart"} {
			t.Run(driver+" "+operation, func(t *testing.T) {
				command := exec.CommandContext(t.Context(), sh, "-s")
				command.Env = append(command.Environ(), "TEST_DRIVER="+driver, "TEST_OPERATION="+operation)
				command.Stdin = strings.NewReader("set -eu\n" + mock + create + "\n${TEST_OPERATION}_sandbox ignored\n")
				output, err := command.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "restricted network is only supported by BoxLite") {
					t.Fatalf("restricted %s network was not rejected for %s: (%q, %v)", driver, operation, output, err)
				}
			})
		}
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
