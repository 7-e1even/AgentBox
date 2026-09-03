package workerscript

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func cancellationShell(t *testing.T, script string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available")
	}
	_, helpers, found := strings.Cut(workerDaemon, "\nrecord_create_resource() {")
	if !found {
		t.Fatal("missing cancellation helpers")
	}
	helpers, _, found = strings.Cut(helpers, "\nprocess_job() {")
	if !found {
		t.Fatal("missing cancellation helper boundary")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, "-s")
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader("set -eu\nrecord_create_resource() {" + helpers + "\n" + script)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("cancellation shell: %v\n%s\n%s", err, out, &stderr)
	}
	return string(out)
}

func TestCreateCancellationSupervisor(t *testing.T) {
	for _, scenario := range []string{"cancel", "success", "finish-race", "cleanup-failed", "ack-retry", "restart-before-ack", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			out := cancellationShell(t, `
STATE_DIR=$(mktemp -d)
trap 'rm -rf "$STATE_DIR"' EXIT
JOB_FILE="$STATE_DIR/input"
printf '{}' > "$JOB_FILE"
JOB_ID=test-job
LEASE_GENERATION=1
SCENARIO=`+scenario+`
export STATE_DIR SCENARIO
sleep() { command sleep 0.05; }
# MSYS and Linux both provide this ps format. The production helper receives
# only the pid/ppid columns it requests from Linux procps.
ps() { command ps -ef | awk 'NR > 1 { print $2, $3 }'; }
poll_create_control() {
  if [ "$SCENARIO" = legacy ]; then printf unsupported; return; fi
  if [ "$SCENARIO" = success ]; then printf continue; return; fi
  if [ -f "$STATE_DIR/started" ] || [ -f "$STATE_DIR/rejected-complete" ]; then printf cancel; else printf continue; fi
}
create_sandbox() {
  if [ "$SCENARIO" = success ] || [ "$SCENARIO" = legacy ] || [ "$SCENARIO" = finish-race ]; then
    printf sandbox-id
    return
  fi
  # Model an installer that retries on a failed command. A cancelled job must
  # kill both the waiting command and this retrying parent.
  : > "$STATE_DIR/started"
  if command sleep 10; then :; else printf retry > "$STATE_DIR/retried"; fi
  printf unexpected > "$STATE_DIR/continued"
}
cleanup_cancelled_create() {
  printf cleanup >> "$STATE_DIR/events"
  [ "$SCENARIO" != cleanup-failed ]
}
complete_job() {
  if [ "$SCENARIO" = finish-race ]; then : > "$STATE_DIR/rejected-complete"; return 1; fi
  printf success >> "$STATE_DIR/events"
}
complete_job_failure() {
  if [ "$SCENARIO" = restart-before-ack ] && [ ! -f "$STATE_DIR/ack-failed" ]; then
    : > "$STATE_DIR/ack-failed"
    exit 42
  fi
  if [ "$SCENARIO" = ack-retry ] && [ ! -f "$STATE_DIR/ack-failed" ]; then
    : > "$STATE_DIR/ack-failed"
    return 1
  fi
  printf ':ack-%s' "$2" >> "$STATE_DIR/events"
}
worker_error_stage() { printf create; }
worker_error_retryable() { printf false; }
if [ "$SCENARIO" = restart-before-ack ]; then
  if process_create_job; then exit 43; fi
  jq() { case "$*" in *leaseGeneration*) printf 1 ;; *) printf test-job ;; esac; }
  recover_create_jobs
else
  process_create_job
fi
test ! -f "$STATE_DIR/retried"
test ! -f "$STATE_DIR/continued"
for journal in "$STATE_DIR"/create-*; do
  if [ "$SCENARIO" = cleanup-failed ]; then test -d "$journal"
  else test ! -d "$journal"
  fi
done
cat "$STATE_DIR/events"
`)
			want := "cleanup:ack-job_cancelled"
			switch scenario {
			case "success", "legacy":
				want = "success"
			case "cleanup-failed":
				want = "cleanup:ack-cancellation_cleanup_failed"
			case "restart-before-ack":
				want = "cleanupcleanup:ack-job_cancelled"
			}
			if out != want {
				t.Fatalf("events = %q, want %q", out, want)
			}
		})
	}
}

func TestCreateCancellationCleanupAndRecovery(t *testing.T) {
	out := cancellationShell(t, `
STATE_DIR=$(mktemp -d)
trap 'rm -rf "$STATE_DIR"' EXIT
CREATE_STATE_DIR="$STATE_DIR/create-test"
mkdir "$CREATE_STATE_DIR"
printf '{}' > "$CREATE_STATE_DIR/job.json"
printf 'boxlite-delete\tnew-sandbox\nboxlite-stop\told-sandbox\n' > "$CREATE_STATE_DIR/resources"
runtime_call() {
  printf '%s:%s:%s\n' "$1" "$2" "$3" >> "$STATE_DIR/events"
  case "$2" in
    inspect) [ -f "$STATE_DIR/runtime-present" ] ;;
    delete) rm -f "$STATE_DIR/runtime-present" ;;
  esac
}
cleanup_cancelled_create
grep -q '^boxlite:stop:old-sandbox$' "$STATE_DIR/events"
grep -q '^boxlite:delete:new-sandbox$' "$STATE_DIR/events"
test "$(head -1 "$STATE_DIR/events")" = boxlite:stop:old-sandbox
: > "$CREATE_STATE_DIR/build-create-pending"
if cleanup_cancelled_create; then exit 40; fi
test -f "$CREATE_STATE_DIR/build-create-pending.settling"
rm "$CREATE_STATE_DIR/build-create-pending" "$CREATE_STATE_DIR/build-create-pending.settling"
# Recover a cancelled lease after the service has restarted. Cleanup is
# idempotent and acknowledgement, rather than PID state, removes the journal.
jq() { case "$*" in *leaseGeneration*) printf 1 ;; *) printf test-job ;; esac; }
poll_create_control() {
  if [ ! -f "$STATE_DIR/connected" ]; then printf unavailable
  elif [ -f "$STATE_DIR/ack" ]; then printf rejected
  else printf cancel
  fi
}
complete_job_failure() { printf '%s' "$2" > "$STATE_DIR/ack"; }
recover_create_jobs
test -d "$CREATE_STATE_DIR"
test ! -f "$STATE_DIR/ack"
: > "$CREATE_STATE_DIR/runtime-create-pending"
AGENTBOX_CREATE_PENDING_SETTLE_SECONDS=0
export AGENTBOX_CREATE_PENDING_SETTLE_SECONDS
: > "$STATE_DIR/runtime-present"
: > "$STATE_DIR/connected"
recover_create_jobs
test "$(cat "$STATE_DIR/ack")" = cancellation_cleanup_failed
test -d "$CREATE_STATE_DIR"
test -f "$CREATE_STATE_DIR/runtime-create-pending.settling"
recover_create_jobs
test "$(cat "$STATE_DIR/ack")" = cancellation_cleanup_failed
test ! -d "$CREATE_STATE_DIR"
printf recovered
`)
	if out != "recovered" {
		t.Fatalf("recovery output = %q", out)
	}
}

func TestCreateCancellationPreservesSharedBoxliteProcess(t *testing.T) {
	out := cancellationShell(t, `
STATE_DIR=$(mktemp -d)
trap 'rm -rf "$STATE_DIR"' EXIT
BOXLITE_SERVER_PID_FILE="$STATE_DIR/shared.pid"
printf 1234 > "$BOXLITE_SERVER_PID_FILE"
kill() { printf '%s\n' "$*"; }
ps() { printf '1234 100\n5678 100\n'; }
kill_create_process_tree 100
`)
	if strings.Contains(out, "1234") || !strings.Contains(out, "-KILL 5678") || !strings.Contains(out, "-KILL 100") {
		t.Fatalf("unexpected process signals: %q", out)
	}
}

func TestDockerVolumeIsJournaledBeforeCreation(t *testing.T) {
	record := strings.Index(workerDaemon, `record_create_resource docker-volume "$VOLUME" "$SANDBOX_ID"`)
	create := strings.Index(workerDaemon, `command docker volume create --label "agentbox.sandbox=$SANDBOX_ID" "$VOLUME"`)
	if record < 0 || create < 0 || record > create {
		t.Fatal("Docker workspace volume must be journaled before it can be created")
	}
}
