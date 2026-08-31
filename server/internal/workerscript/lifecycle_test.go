package workerscript

import (
	"os/exec"
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
