package workerscript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestWorkerHeartbeatInventoryRefresh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not available on this test host")
	}
	_, heartbeat, found := strings.Cut(workerDaemon, "\nmark_worker_inventory_dirty() {")
	if !found {
		t.Fatal("Worker inventory invalidation function is missing")
	}
	heartbeat, _, found = strings.Cut(heartbeat, "\nrun_worker() {")
	if !found {
		t.Fatal("Worker heartbeat function boundary is missing")
	}
	heartbeat = "mark_worker_inventory_dirty() {" + heartbeat
	for _, test := range []struct {
		name                 string
		rounds               int
		wantInventoryRounds  []int
		wantRequests         int
		wantCollectionRounds []int
	}{
		{"periodic", 10, []int{0, 8}, 10, nil},
		{"event", 5, []int{0, 2}, 5, nil},
		{"event-during-collection", 3, []int{0, 1}, 3, nil},
		{"event-during-report", 3, []int{0, 1}, 3, nil},
		{"failed-report", 3, []int{0, 1}, 4, nil},
		{"failed-event-report", 5, []int{0, 2, 3}, 6, nil},
		{"legacy-server", 3, []int{0}, 4, nil},
		{"failed-collection", 3, []int{1}, 3, []int{0, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			// Run the real heartbeat and atomic invalidation functions. Only
			// runtime discovery, time and HTTP are replaced; jq remains real.
			fixture := `
STATE_DIR=$TEST_STATE_DIR
SERVER_URL=https://unused.invalid
SERVER_ID=fixture
CREDENTIAL=fixture-credential
AGENTBOX_WORKER_VERSION=test-version
FIXTURE_NOW=1000
FIXTURE_ROUND=0
FIXTURE_ATTEMPT=0
if command -v jq.exe >/dev/null 2>&1; then
  jq() { command jq.exe -b "$@"; }
fi
date() { printf '%s' "$FIXTURE_NOW"; }
worker_capabilities() { printf '["docker"]'; }
worker_inventory() {
  [ "$1" = '["docker"]' ] || exit 91
  printf '%s\n' "$FIXTURE_ROUND" >> "$STATE_DIR/collections"
  if [ "$TEST_CASE" = failed-collection ] && [ "$FIXTURE_ROUND" -eq 0 ]; then
    return 1
  fi
  if [ "$TEST_CASE" = event-during-collection ] && [ "$FIXTURE_ROUND" -eq 0 ]; then
    mark_worker_inventory_dirty
  fi
  printf '{"dockerImages":[{"id":"image-%s"}]}' "$FIXTURE_ROUND"
}
curl() {
  FIXTURE_ATTEMPT=$((FIXTURE_ATTEMPT + 1))
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --data) FIXTURE_BODY=$2; shift 2 ;;
      *) shift ;;
    esac
  done
  printf '%s' "$FIXTURE_BODY" | jq -c --argjson round "$FIXTURE_ROUND" \
    '{round:$round,body:.}' >> "$STATE_DIR/requests"
  if [ "$TEST_CASE" = event-during-report ] && [ "$FIXTURE_ROUND" -eq 0 ]; then
    mark_worker_inventory_dirty
  fi
  case "$TEST_CASE:$FIXTURE_ROUND" in
    failed-report:0|failed-event-report:2) return 1 ;;
    legacy-server:0) [ "$FIXTURE_ATTEMPT" -ne 1 ] ;;
  esac
}
worker_origin_curl() { curl "$@"; }
sleep() {
  [ "$1" = 15 ] || exit 92
  FIXTURE_ROUND=$((FIXTURE_ROUND + 1))
  FIXTURE_NOW=$((FIXTURE_NOW + 15))
  FIXTURE_ATTEMPT=0
  [ "$FIXTURE_ROUND" -lt "$TEST_ROUNDS" ] || exit 0
  case "$TEST_CASE:$FIXTURE_ROUND" in
    event:2|failed-event-report:2) mark_worker_inventory_dirty ;;
  esac
}
`
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATE_DIR="+filepath.ToSlash(stateDir),
				"TEST_CASE="+test.name, "TEST_ROUNDS="+strconv.Itoa(test.rounds))
			command.Stdin = strings.NewReader("set -eu\n" + fixture + heartbeat + "\nheartbeat_loop\n")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("heartbeat fixture: %v\n%s", err, output)
			}
			collections, err := os.ReadFile(filepath.Join(stateDir, "collections"))
			if err != nil {
				t.Fatal(err)
			}
			var gotInventoryRounds []int
			for value := range strings.FieldsSeq(string(collections)) {
				round, err := strconv.Atoi(value)
				if err != nil {
					t.Fatal(err)
				}
				gotInventoryRounds = append(gotInventoryRounds, round)
			}
			wantCollectionRounds := test.wantCollectionRounds
			if wantCollectionRounds == nil {
				wantCollectionRounds = test.wantInventoryRounds
			}
			if !slices.Equal(gotInventoryRounds, wantCollectionRounds) {
				t.Fatalf("inventory collected at rounds %v, want %v", gotInventoryRounds, wantCollectionRounds)
			}
			requests, err := os.ReadFile(filepath.Join(stateDir, "requests"))
			if err != nil {
				t.Fatal(err)
			}
			requestCount := 0
			seenRounds := make(map[int]bool)
			for line := range strings.SplitSeq(strings.TrimSpace(string(requests)), "\n") {
				var request struct {
					Round int                        `json:"round"`
					Body  map[string]json.RawMessage `json:"body"`
				}
				if err := json.Unmarshal([]byte(line), &request); err != nil {
					t.Fatal(err)
				}
				_, hasInventory := request.Body["inventory"]
				if wantInventory := slices.Contains(test.wantInventoryRounds, request.Round); hasInventory != wantInventory {
					t.Fatalf("round %d inventory present = %v, want %v: %s", request.Round, hasInventory, wantInventory, line)
				}
				var capabilities []string
				if err := json.Unmarshal(request.Body["capabilities"], &capabilities); err != nil || !slices.Equal(capabilities, []string{"docker"}) {
					t.Fatalf("round %d capabilities = %s, error = %v", request.Round, request.Body["capabilities"], err)
				}
				seenRounds[request.Round] = true
				requestCount++
			}
			if requestCount != test.wantRequests || len(seenRounds) != test.rounds {
				t.Fatalf("requests = %d across %d rounds, want %d across %d", requestCount, len(seenRounds), test.wantRequests, test.rounds)
			}
		})
	}
}

func TestWorkerInventoryReusesDockerCapabilityProbe(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available on this test host")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not available on this test host")
	}
	_, discovery, found := strings.Cut(workerDaemon, "\nworker_capabilities() {")
	if !found {
		t.Fatal("Worker capabilities function is missing")
	}
	discovery, _, found = strings.Cut(discovery, "\n# Runs in a subshell")
	if !found {
		t.Fatal("Worker inventory function boundary is missing")
	}
	discovery = "worker_capabilities() {" + discovery
	for _, available := range []bool{true, false} {
		t.Run(strconv.FormatBool(available), func(t *testing.T) {
			stateDir := t.TempDir()
			fixture := `
STATE_DIR=$TEST_STATE_DIR
BOXLITE_IMAGES_FILE=$TEST_STATE_DIR/no-boxlite-images
AGENTBOX_VM_IMAGE_DIR=$TEST_STATE_DIR/no-vm-images
if command -v jq.exe >/dev/null 2>&1; then
  jq() { command jq.exe -b "$@"; }
fi
worker_arch() { printf amd64; }
runtime_probe() { return 1; }
runtime_call() { printf '[]'; }
timeout() { shift; "$@"; }
docker() {
  printf '%s\n' "$*" >> "$TEST_STATE_DIR/docker-calls"
  case "$1" in
    info) [ "$TEST_DOCKER_AVAILABLE" = true ] ;;
    image) printf '{"ID":"image-id","Repository":"fixture","Tag":"latest","Size":"1MB","CreatedSince":"1 day ago"}\n' ;;
    *) return 93 ;;
  esac
}
`
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_STATE_DIR="+filepath.ToSlash(stateDir),
				"TEST_DOCKER_AVAILABLE="+strconv.FormatBool(available))
			command.Stdin = strings.NewReader("set -eu\n" + fixture + discovery + "\nCAPS=$(worker_capabilities)\nprintf '%s\\n' \"$CAPS\" > \"$TEST_STATE_DIR/capabilities\"\nworker_inventory \"$CAPS\"\n")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("inventory fixture: %v\n%s", err, output)
			}
			var inventory struct {
				DockerImages []struct{ ID string } `json:"dockerImages"`
			}
			if err := json.Unmarshal(output, &inventory); err != nil {
				t.Fatalf("decode inventory: %v\n%s", err, output)
			}
			wantImageCount := 0
			if available {
				wantImageCount = 1
			}
			if len(inventory.DockerImages) != wantImageCount {
				t.Fatalf("Docker image count = %d, want %d", len(inventory.DockerImages), wantImageCount)
			}
			encodedCapabilities, err := os.ReadFile(filepath.Join(stateDir, "capabilities"))
			if err != nil {
				t.Fatal(err)
			}
			var capabilities []string
			if err := json.Unmarshal(encodedCapabilities, &capabilities); err != nil {
				t.Fatalf("decode capabilities: %v: %s", err, encodedCapabilities)
			}
			for _, capability := range []string{"mcp-managed-config", "managed-capability-config", "fail-closed-job-output"} {
				if !slices.Contains(capabilities, capability) {
					t.Fatalf("Worker capabilities %v do not include %q", capabilities, capability)
				}
			}
			calls, err := os.ReadFile(filepath.Join(stateDir, "docker-calls"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if len(lines) != 1+wantImageCount || lines[0] != "info" {
				t.Fatalf("Docker discovery calls = %q; expected one info probe and %d image listing", calls, wantImageCount)
			}
		})
	}
}
