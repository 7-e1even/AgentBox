package workerscript

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"agentbox/internal/workerprotocol"
)

func TestWorkerProtocolHeadersMatchSharedContract(t *testing.T) {
	for _, value := range []string{
		fmt.Sprintf("%s: %d", workerprotocol.HeaderMinimum, workerprotocol.Minimum),
		fmt.Sprintf("%s: %d", workerprotocol.HeaderMaximum, workerprotocol.Current),
	} {
		if !strings.Contains(workerDaemon, value) {
			t.Fatalf("Worker script does not offer %q", value)
		}
	}
}

func TestWorkerRequestValidatesActualShellResponses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	start := strings.Index(workerDaemon, "worker_request() (")
	end := strings.Index(workerDaemon, "\ncomplete_job() {")
	if start < 0 || end <= start {
		t.Fatal("Worker request function is missing")
	}
	helper := workerDaemon[start:end]
	for _, test := range []struct {
		name, selected, status, wantStatus string
		wantError                          bool
	}{
		{name: "current", selected: "1", status: "200", wantStatus: "200"},
		{name: "n-1 Server", status: "200", wantStatus: "200"},
		{name: "completion", selected: "1", status: "204", wantStatus: "204"},
		{name: "unsupported selection", selected: "2", status: "200", wantStatus: "426", wantError: true},
		{name: "malformed selection", selected: "1 extra", status: "200", wantStatus: "426", wantError: true},
		{name: "rejected offer", status: "426", wantStatus: "426", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The real shell function runs unchanged. Only curl is replaced, so no
			// live host, credential, job or runtime side effect is involved.
			mock := `curl() {
  OFFER_MIN=
  OFFER_MAX=
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -D) HEADERS=$2; shift 2 ;;
      -H)
        case "$2" in
          'X-AgentBox-Worker-Protocol-Min: 1') OFFER_MIN=1 ;;
          'X-AgentBox-Worker-Protocol-Max: 1') OFFER_MAX=1 ;;
        esac
        shift 2 ;;
      *) shift ;;
    esac
  done
  [ "$OFFER_MIN:$OFFER_MAX" = 1:1 ] || return 90
  printf 'HTTP/1.1 %s\r\n' "$TEST_STATUS" > "$HEADERS"
  if [ -n "$TEST_SELECTED" ]; then
    printf 'X-AgentBox-Worker-Protocol: %s\r\n' "$TEST_SELECTED" >> "$HEADERS"
  fi
  printf '%s' "$TEST_STATUS"
}
worker_origin_curl() { curl "$@"; }
`
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATUS="+test.status, "TEST_SELECTED="+test.selected)
			command.Stdin = strings.NewReader("set -eu\n" + mock + helper + "\nworker_request /dev/null -X POST https://unused.invalid\n")
			output, err := command.Output()
			if (err != nil) != test.wantError || string(output) != test.wantStatus {
				t.Fatalf("request = (%q, %v), want (%q, error=%v)", output, err, test.wantStatus, test.wantError)
			}
		})
	}
}
