package workerscript

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type extensionProgressReport struct {
	ID     string `json:"extensionId"`
	Status string `json:"extensionStatus"`
	Output string `json:"extensionOutput"`
}

func extensionShellFunctions(t *testing.T) (string, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not available")
	}
	_, functions, found := strings.Cut(workerDaemon, "\nprepare_job_variable_snapshot() (")
	if !found {
		t.Fatal("extension functions are missing")
	}
	functions, _, found = strings.Cut(functions, "\ncreate_sandbox() {")
	if !found {
		t.Fatal("extension function boundary is missing")
	}
	_, errorHelpers, found := strings.Cut(workerDaemon, "\nworker_error_stage_from_log() {")
	if !found {
		t.Fatal("safe error helpers are missing")
	}
	errorHelpers, _, found = strings.Cut(errorHelpers, "\nreport_job_progress() {")
	if !found {
		t.Fatal("safe error helper boundary is missing")
	}
	return sh, "worker_error_stage_from_log() {" + errorHelpers + "\nprepare_job_variable_snapshot() (" + functions
}

func writeExtensionTestJob(t *testing.T, root string, extensions []map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"job": map[string]any{"action": "create-sandbox", "payload": map[string]any{
		"workdir": "/workspace", "extensions": extensions,
		"environmentVariables": []map[string]string{{"name": "API_SECRET", "value": "env-secret-'$-value"}},
		"credentials":          []map[string]string{{"secret": "credential-secret", "token": "credential-token"}},
		"proxy": map[string]any{
			"url":             "https://proxy-user:proxy-pass%21@proxy.invalid:8443",
			"redactionValues": []string{"proxy-user", "proxy-pass!", "代理密码"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "job.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func extensionDefinition(id, install, verify string) map[string]any {
	return map[string]any{"id": id, "generation": 3, "spec": map[string]any{
		"version": "1.2.3", "installScript": install, "verifyScript": verify, "timeoutSeconds": 30,
	}}
}

func runExtensionFixture(t *testing.T, root, job, invoke string, missingTimeout bool) ([]byte, error, []extensionProgressReport) {
	t.Helper()
	sh, functions := extensionShellFunctions(t)
	reportPath := filepath.Join(root, "reports.jsonl")
	// Execute the actual guest shell and timeout in a disposable filesystem.
	// Only the transport and progress HTTP request are replaced; no Worker runs.
	mock := `
docker() (
  [ "$1" = exec ] || exit 91
  shift
  [ "$1" != -i ] || shift
  if [ "$1" = -w ]; then [ "$2" = /workspace ] || exit 92; shift 2; fi
  [ "$1" = fixture ] || exit 93
  shift
  [ "$1" = sh ] && [ "$2" = -c ] || exit 94
  GUEST_COMMAND=$(printf '%s' "$3" | sed -e 's|/opt/agentbox/secrets|./secrets|g' -e 's|/tmp/agentbox-extension-|./agentbox-extension-|g')
  shift 3
  if [ "$MISSING_TIMEOUT" = true ]; then GUEST_COMMAND="PATH=/nonexistent; $GUEST_COMMAND"; fi
  cd "$TEST_ROOT"
  sh -c "$GUEST_COMMAND" "$@"
)
worker_request() {
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --data ]; then printf '%s' "$2" | jq -c . >> "$TEST_REPORTS"; printf 200; return; fi
    shift
  done
  return 95
}
resolve_worker_variables() { printf '{"RESOLVED_SECRET":"resolved-worker-secret"}\n' > "$2"; }
sleep() { command sleep 0.05; }
JOB_ID=fixture-job
LEASE_GENERATION=1
SERVER_ID=fixture-server
SERVER_URL=https://unused.invalid
CREDENTIAL=unused-worker-token
`
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	command := exec.CommandContext(t.Context(), sh, "-s")
	command.Dir = root
	command.Env = append(command.Environ(), "TEST_ROOT="+filepath.ToSlash(root),
		"TEST_JOB="+filepath.ToSlash(job), "TEST_REPORTS="+filepath.ToSlash(reportPath),
		"MISSING_TIMEOUT="+map[bool]string{true: "true", false: "false"}[missingTimeout],
		"TMPDIR="+filepath.ToSlash(root))
	command.Stdin = strings.NewReader("set -eu\n" + mock + functions + "\n" + invoke)
	output, runErr := command.CombinedOutput()
	var reports []extensionProgressReport
	if encoded, err := os.ReadFile(reportPath); err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(encoded)), "\n") {
			if line == "" {
				continue
			}
			var report extensionProgressReport
			if err := json.Unmarshal([]byte(line), &report); err != nil {
				t.Fatalf("invalid progress: %v: %s", err, line)
			}
			reports = append(reports, report)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return output, runErr, reports
}

func TestExtensionsInstallAndVerifyInOrderWithoutExposingSecrets(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "agentbox.env"), []byte("API_SECRET='env-secret-'\"'\"'$-value'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	firstInstall := `printf 'first-install\n' >> "$TEST_ROOT/order"
printf '%s\n' "$API_SECRET" 'credential-secret' 'credential-token' 'proxy-user' 'proxy-pass!' 'proxy-pass%21' '代理密码' 'resolved-worker-secret'
printf '%s\n' 'literal-$(touch host-leak)' > "$TEST_ROOT/literal"
`
	job := writeExtensionTestJob(t, root, []map[string]any{
		extensionDefinition("first", firstInstall, `printf 'first-verify\n' >> "$TEST_ROOT/order"; test -f "$TEST_ROOT/literal"`),
		extensionDefinition("second", `printf 'second-install\n' >> "$TEST_ROOT/order"`, `printf 'second-verify\n' >> "$TEST_ROOT/order"`),
	})
	output, err, reports := runExtensionFixture(t, root, job, `
if result=$(install_sandbox_extensions fixture "$TEST_JOB"); then printf success; else exit 1; fi
`, false)
	if err != nil || string(output) != "success" {
		t.Fatalf("installation = (%q, %v)", output, err)
	}
	order, err := os.ReadFile(filepath.Join(root, "order"))
	if err != nil || string(order) != "first-install\nfirst-verify\nsecond-install\nsecond-verify\n" {
		t.Fatalf("wrong execution order: %q, %v", order, err)
	}
	literal, err := os.ReadFile(filepath.Join(root, "literal"))
	if err != nil || string(literal) != "literal-$(touch host-leak)\n" {
		t.Fatalf("script text was reinterpreted: %q, %v", literal, err)
	}
	if _, err := os.Stat(filepath.Join(root, "host-leak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("script was interpolated by the host shell: %v", err)
	}
	completed := 0
	foundWithheld := false
	for _, report := range reports {
		if report.Status == "succeeded" {
			completed++
		}
		foundWithheld = foundWithheld || report.Output == "[extension output withheld: Worker does not export sandbox output]"
		for _, secret := range []string{"env-secret-'$-value", "credential-secret", "credential-token", "proxy-user", "proxy-pass!", "proxy-pass%21", "代理密码", "resolved-worker-secret"} {
			if strings.Contains(report.Output, secret) {
				t.Fatalf("progress exposed a sensitive value: %s", report.ID)
			}
		}
		if len(report.Output) > 4096 {
			t.Fatalf("extension output exceeds its byte limit: %d", len(report.Output))
		}
	}
	if completed != 2 || !foundWithheld {
		t.Fatalf("missing completions or protected output withholding: %#v", reports)
	}
}

func TestExtensionsStopAfterFailedInstallVerifyOrMissingGuestTimeout(t *testing.T) {
	for _, test := range []struct {
		name            string
		install, verify string
		missingTimeout  bool
		wantOrder       string
	}{
		{"install", `printf 'install\n' >> "$TEST_ROOT/order"; printf credential-secret >&2; exit 7`, `printf 'verify\n' >> "$TEST_ROOT/order"`, false, "install\n"},
		{"verify", `printf 'install\n' >> "$TEST_ROOT/order"`, `printf 'verify\n' >> "$TEST_ROOT/order"; printf credential-token >&2; exit 8`, false, "install\nverify\n"},
		{"timeout unavailable", `printf 'install\n' >> "$TEST_ROOT/order"`, "true", true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			job := writeExtensionTestJob(t, root, []map[string]any{
				extensionDefinition("first", test.install, test.verify),
				extensionDefinition("must-not-run", `printf 'unexpected\n' >> "$TEST_ROOT/order"`, "true"),
			})
			output, err, reports := runExtensionFixture(t, root, job, `
if result=$(install_sandbox_extensions fixture "$TEST_JOB"); then printf unexpected-success; else exit 1; fi
`, test.missingTimeout)
			if err == nil || strings.Contains(string(output), "unexpected-success") || strings.Contains(string(output), "credential-") {
				t.Fatalf("unsafe failure result: %q, %v", output, err)
			}
			order, readErr := os.ReadFile(filepath.Join(root, "order"))
			if (readErr != nil && !errors.Is(readErr, os.ErrNotExist)) || string(order) != test.wantOrder {
				t.Fatalf("execution continued after failure: %q, %v", order, readErr)
			}
			if len(reports) == 0 || reports[len(reports)-1].Status != "failed" {
				t.Fatalf("failure was not reported: %#v", reports)
			}
			for _, report := range reports {
				if report.ID == "must-not-run" || strings.Contains(report.Output, "credential-") {
					t.Fatalf("unsafe progress after failure: %#v", report)
				}
			}
		})
	}
}

func TestExtensionTimeoutKillsGuestStepChildren(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, nil)
	script := `(trap '' TERM; sleep 2; printf orphan > "$TEST_ROOT/orphan") &
wait
`
	if err := os.WriteFile(filepath.Join(root, "step.sh"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job, `
if run_extension_step fixture "$TEST_JOB" first installing "$TEST_ROOT/step.sh" "$TEST_ROOT/output.log" 1 /workspace; then
  printf unexpected-success
else
  exit 1
fi
`, false)
	if err == nil || strings.Contains(string(output), "unexpected-success") {
		t.Fatalf("guest timeout did not fail the step: %q, %v", output, err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "orphan")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a child survived the guest timeout: %v", err)
	}
}

func TestExtensionOutputCollectionIsBoundedAndWithheldAtLimit(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, nil)
	// The cap cuts through a sensitive value. None of the capped log is public.
	script := "head -c 1048568 /dev/zero\nprintf credential-secret\nsleep 3\nprintf orphan > \"$TEST_ROOT/output-orphan\"\n"
	if err := os.WriteFile(filepath.Join(root, "step.sh"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job, `
if run_extension_step fixture "$TEST_JOB" first installing "$TEST_ROOT/step.sh" "$TEST_ROOT/output.log" 5 /workspace; then
  printf unexpected-success
else
  redact_extension_output "$TEST_JOB" "$TEST_ROOT/output.log"
  exit 1
fi
`, false)
	if err == nil || !strings.Contains(string(output), "does not export sandbox output") || strings.Contains(string(output), "credential-") {
		t.Fatalf("output cap did not safely fail: %q, %v", output, err)
	}
	info, err := os.Stat(filepath.Join(root, "output.log"))
	if err != nil || info.Size() != 1<<20 {
		t.Fatalf("raw log was not bounded: %v, %v", info, err)
	}
	time.Sleep(3200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "output-orphan")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the guest step survived its output limit: %v", err)
	}
}

func TestExtensionVerificationRequiresServerAcknowledgement(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, []map[string]any{extensionDefinition("first", "true", "true")})
	output, err, _ := runExtensionFixture(t, root, job, `
worker_request() { printf 503; }
if result=$(install_sandbox_extensions fixture "$TEST_JOB"); then printf unexpected-success; else exit 1; fi
`, false)
	if err == nil || !strings.Contains(string(output), "did not acknowledge extension verification") {
		t.Fatalf("unacknowledged verification reported success: %q, %v", output, err)
	}
}

func TestStartWithoutExternalIDDoesNotReinstallExtensions(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, []map[string]any{extensionDefinition("first", "true", "true")})
	encoded, err := os.ReadFile(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job, []byte(strings.Replace(string(encoded), `"action":"create-sandbox"`, `"action":"start-sandbox"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	_, create, found := strings.Cut(workerDaemon, "\ncreate_sandbox() {")
	if !found {
		t.Fatal("create_sandbox is missing")
	}
	create, _, _ = strings.Cut(create, "\nsandbox_container_name() {")
	_, start, found := strings.Cut(workerDaemon, "\nstart_sandbox() {")
	if !found {
		t.Fatal("start_sandbox is missing")
	}
	start, _, _ = strings.Cut(start, "\nrestart_sandbox() {")
	invoke := "create_sandbox() {" + create + "\nstart_sandbox() {" + start + `
if result=$(start_sandbox "$TEST_JOB"); then printf unexpected-success; else exit 1; fi
`
	output, err, reports := runExtensionFixture(t, root, job, invoke, false)
	if err == nil || !strings.Contains(string(output), "extensions can only run in a new create-sandbox job") || len(reports) != 0 {
		t.Fatalf("start retried extension installation: %q, %v, %#v", output, err, reports)
	}
}

func TestExtensionExecShimPreservesStdinWorkdirAndArguments(t *testing.T) {
	sh, _ := extensionShellFunctions(t)
	_, shim, found := strings.Cut(workerDaemon, "\ndocker() {")
	if !found {
		t.Fatal("runtime exec shim is missing")
	}
	shim, _, _ = strings.Cut(shim, "\nworker_capabilities() {")
	_, boxlite, found := strings.Cut(workerDaemon, "\nboxlite_call() {")
	if !found {
		t.Fatal("BoxLite exec shim is missing")
	}
	boxlite, _, _ = strings.Cut(boxlite, "\nruntime_call() {")
	for _, driver := range []string{"boxlite", "microsandbox"} {
		t.Run(driver, func(t *testing.T) {
			mock := `
boxlite_cli() { printf '%s\n' "$@"; cat; }
runtime_call() {
  if [ "$1" = boxlite ]; then shift; boxlite_call "$@"; else printf '%s\n' "$@"; cat; fi
}
`
			command := exec.CommandContext(t.Context(), sh, "-c", "set -eu\n"+mock+"boxlite_call() {"+boxlite+"\ndocker() {"+shim+`
docker exec -i -w '/workspace with spaces' fixture sh -c 'literal $(must-not-execute)'
`)
			command.Env = append(command.Environ(), "AGENTBOX_RUNTIME_DRIVER="+driver)
			command.Stdin = strings.NewReader("script from stdin\n")
			output, err := command.CombinedOutput()
			prefix := "microsandbox\nexec\n--stdin\n--workdir\n"
			if driver == "boxlite" {
				prefix = "exec\n-i\n-u\n0:0\n-w\n"
			}
			want := prefix + "/workspace with spaces\nfixture\n--\nsh\n-c\nliteral $(must-not-execute)\nscript from stdin\n"
			if err != nil || string(output) != want {
				t.Fatalf("exec arguments or stdin changed: %q, %v", output, err)
			}
		})
	}
}

func TestProtectedJobOutputIsWithheldAcrossEncodingsPrefixesAndControls(t *testing.T) {
	root := t.TempDir()
	secret := "token +/value"
	encoded, err := json.Marshal(map[string]any{"job": map[string]any{"payload": map[string]any{
		"environmentVariables": []map[string]string{{"name": "TOKEN", "value": secret}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, "job.json")
	if err := os.WriteFile(job, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	uriSecret := strings.ReplaceAll(url.PathEscape(secret), "+", "%2B")
	formSecret := url.QueryEscape(secret)
	base64Secret := base64.StdEncoding.EncodeToString([]byte(secret))
	base64URLSecret := base64.RawURLEncoding.EncodeToString([]byte(secret))
	log := strings.Join([]string{
		secret,
		uriSecret,
		strings.ReplaceAll(strings.ReplaceAll(uriSecret, "%2B", "%2b"), "%2F", "%2f"),
		formSecret,
		strings.ToLower(formSecret),
		base64Secret,
		base64URLSecret,
		"token +/\n",
		"token +/\x00value",
	}, " | ")
	if err := os.WriteFile(filepath.Join(root, "output.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job, `redact_extension_output "$TEST_JOB" "$TEST_ROOT/output.log"`, false)
	result := strings.TrimSpace(string(output))
	if err != nil || result != "[extension output withheld: Worker does not export sandbox output]" {
		t.Fatalf("protected output was not withheld: %q, %v", output, err)
	}
	for _, variant := range []string{secret, uriSecret, formSecret, base64Secret, base64URLSecret, "token +/"} {
		if strings.Contains(result, variant) {
			t.Fatalf("protected output exposed variant %q", variant)
		}
	}
}

func TestJobOutputIsWithheldEvenWithoutKnownPayloadSecrets(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "job.json")
	snapshot := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(job, []byte(`{"job":{"payload":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "output.log")
	if err := os.WriteFile(logPath, []byte(strings.Repeat("safe-output-", 500)), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job,
		`redact_job_output "$TEST_JOB" "$TEST_ROOT/output.log" "$TEST_ROOT/snapshot.json" 3500 true`, false)
	if err != nil || strings.TrimSpace(string(output)) != "[job output withheld: Worker does not export command output]" {
		t.Fatalf("nonsensitive-looking output crossed the Worker boundary: (%q, %v)", output, err)
	}
	if err := os.WriteFile(logPath, []byte("token-prefix\x00secret-suffix"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ = runExtensionFixture(t, root, job,
		`redact_job_output "$TEST_JOB" "$TEST_ROOT/output.log" "$TEST_ROOT/snapshot.json" 3500 true`, false)
	if err != nil || strings.TrimSpace(string(output)) != "[job output withheld: Worker does not export command output]" {
		t.Fatalf("control-bearing output was not withheld: %q, %v", output, err)
	}
}

func TestBuiltInSandboxMarkerDoesNotEnableRawDiagnostics(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "job.json")
	snapshot := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(job, []byte(`{"job":{"payload":{"environmentVariables":[{"name":"IS_SANDBOX","value":"1"}]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	const diagnostic = "stage runtime-probe failed: runtime executable is unavailable"
	if err := os.WriteFile(filepath.Join(root, "output.log"), []byte(diagnostic), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job,
		`redact_job_output "$TEST_JOB" "$TEST_ROOT/output.log" "$TEST_ROOT/snapshot.json" 3500 true`, false)
	if err != nil || strings.TrimSpace(string(output)) != "[job output withheld: Worker does not export command output]" {
		t.Fatalf("built-in sandbox marker enabled raw diagnostics: (%q, %v)", output, err)
	}
}

func TestExtensionRedactionUsesProvisioningSnapshotAcrossRotation(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, nil)
	const oldSecret = "old-provisioned-worker-secret"
	if err := os.WriteFile(filepath.Join(root, "snapshot.json"), []byte(`{"TOKEN":"`+oldSecret+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "output.log"), []byte("prefix "+oldSecret+" suffix"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job,
		`redact_extension_output "$TEST_JOB" "$TEST_ROOT/output.log" "$TEST_ROOT/snapshot.json"`, false)
	if err != nil || strings.Contains(string(output), oldSecret) || !strings.Contains(string(output), "does not export sandbox output") {
		t.Fatalf("rotated Variable snapshot redaction = (%q, %v)", output, err)
	}
}

func TestJobOutputRedactionProtectsCompletionMessagesAcrossRotation(t *testing.T) {
	root := t.TempDir()
	job := writeExtensionTestJob(t, root, nil)
	const variableSecret = "old-provisioned-worker-secret"
	if err := os.WriteFile(filepath.Join(root, "snapshot.json"), []byte(`{"TOKEN":"`+variableSecret+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	log := strings.Repeat("safe-prefix-", 400) + " env-secret-'$-value credential-secret credential-token " + variableSecret
	if err := os.WriteFile(filepath.Join(root, "output.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	output, err, _ := runExtensionFixture(t, root, job,
		`redact_job_output "$TEST_JOB" "$TEST_ROOT/output.log" "$TEST_ROOT/snapshot.json" 3500 true`, false)
	if err != nil || len(output) > 3500 || !strings.Contains(string(output), "does not export command output") {
		t.Fatalf("completion redaction = (%q, %v)", output, err)
	}
	for _, secret := range []string{"env-secret-'$-value", "credential-secret", "credential-token", variableSecret} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("completion message exposed %q: %q", secret, output)
		}
	}
}

func TestJobCompletionLogsAlwaysUseUnifiedRedaction(t *testing.T) {
	_, create, found := strings.Cut(workerDaemon, "\nprocess_create_job() (")
	if !found {
		t.Fatal("create job supervisor is missing")
	}
	create, process, found := strings.Cut(create, "\nprocess_job() {")
	if !found {
		t.Fatal("generic job processor is missing")
	}
	process, _, found = strings.Cut(process, "\nmark_worker_inventory_dirty() {")
	if !found {
		t.Fatal("generic job processor boundary is missing")
	}
	if strings.Contains(create, `tail -c 3500`) || strings.Contains(create, `redact_job_output "$JOB_FILE" "$CREATE_STATE_DIR/log"`) ||
		!strings.Contains(create, `MESSAGE=$(worker_safe_stage_message "$ERROR_STAGE")`) {
		t.Fatal("create completion can export free-form sandbox output")
	}
	if strings.Contains(process, `tail -c 3500`) || strings.Count(process, `redact_job_output "$JOB_FILE" "$LOG_FILE"`) < 5 {
		t.Fatal("sandbox/configuration job logs can bypass unified redaction")
	}
	if !strings.Contains(workerDaemon, `redact_extension_output "$EXTENSION_JOB"`) ||
		!strings.Contains(workerDaemon, `[extension output withheld: Worker does not export sandbox output]`) {
		t.Fatal("extension progress and final errors can export free-form sandbox output")
	}
}

func TestProcessJobFailureCompletionWithholdsUnverifiableRotatedGuestSecrets(t *testing.T) {
	sh, functions := extensionShellFunctions(t)
	_, process, found := strings.Cut(workerDaemon, "\nprocess_job() {")
	if !found {
		t.Fatal("generic job processor is missing")
	}
	process, _, found = strings.Cut(process, "\nmark_worker_inventory_dirty() {")
	if !found {
		t.Fatal("generic job processor boundary is missing")
	}
	mock := `
resolve_worker_variables() { printf '{"TOKEN":"new-worker-secret"}\n' > "$2"; }
emit_stale_guest_log() {
  printf '%s\n' "env-secret-'\$-value" credential-secret credential-token old-provisioned-worker-secret >&2
  return 1
}
start_sandbox() { emit_stale_guest_log; }
restart_sandbox() { emit_stale_guest_log; }
stop_sandbox() { emit_stale_guest_log; }
delete_sandbox() { emit_stale_guest_log; }
apply_sandbox_proxy() { emit_stale_guest_log; }
sandbox_container_name() { printf fixture; }
update_agent_tools() { printf '[]\n' > "$2"; emit_stale_guest_log; }
complete_job_failure() { printf '%s' "$5" > "$TEST_RESULT"; }
complete_agent_tool_job() { printf '%s' "$3" > "$TEST_RESULT"; }
complete_job() { :; }
mark_worker_inventory_dirty() { :; }
`
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	for _, action := range []string{
		"start-sandbox", "restart-sandbox", "stop-sandbox", "delete-sandbox",
		"configure-sandbox-proxy", "update-sandbox-agent-tools",
	} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			job := writeExtensionTestJob(t, root, nil)
			encoded, err := os.ReadFile(job)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			jobObject := document["job"].(map[string]any)
			jobObject["id"] = "job-one"
			jobObject["leaseGeneration"] = 1
			jobObject["action"] = action
			jobObject["payload"].(map[string]any)["driver"] = "docker"
			jobObject["payload"].(map[string]any)["requestedAgentTools"] = []string{"codex"}
			encoded, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(job, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			result := filepath.Join(root, "completion.txt")
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_RESULT="+filepath.ToSlash(result), "TMPDIR="+filepath.ToSlash(root))
			command.Stdin = strings.NewReader("set -eu\n" + mock + functions + "\nprocess_job() {" + process + "\nprocess_job " + shellPath(job) + "\n")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("process job: %v\n%s", err, output)
			}
			completion, err := os.ReadFile(result)
			if err != nil || (!strings.Contains(string(completion), "仅保留在 Worker 本机") &&
				!strings.Contains(string(completion), "detailed output was withheld")) {
				t.Fatalf("completion = (%q, %v)", completion, err)
			}
			for _, secret := range []string{"env-secret-'$-value", "credential-secret", "credential-token", "old-provisioned-worker-secret", "new-worker-secret"} {
				if strings.Contains(string(completion), secret) {
					t.Fatalf("completion exposed %q: %q", secret, completion)
				}
			}
		})
	}
}

func TestProcessJobSuccessIgnoresOperationStdoutForExternalID(t *testing.T) {
	sh, functions := extensionShellFunctions(t)
	_, process, found := strings.Cut(workerDaemon, "\nprocess_job() {")
	if !found {
		t.Fatal("generic job processor is missing")
	}
	process, _, found = strings.Cut(process, "\nmark_worker_inventory_dirty() {")
	if !found {
		t.Fatal("generic job processor boundary is missing")
	}
	mock := `
resolve_worker_variables() { printf '{}\n' > "$2"; }
start_sandbox() { printf 'successful-guest-stdout-secret'; }
stop_sandbox() { printf 'successful-guest-stdout-secret'; }
restart_sandbox() { printf 'successful-guest-stdout-secret'; }
delete_sandbox() { printf 'successful-guest-stdout-secret'; }
apply_sandbox_proxy() { printf 'successful-guest-stdout-secret'; }
sandbox_container_name() { printf agentbox-fixed; }
complete_job() { printf '%s|%s' "$3" "$4" > "$TEST_RESULT"; }
complete_job_failure() { printf failure > "$TEST_RESULT"; }
mark_worker_inventory_dirty() { :; }
`
	if runtime.GOOS == "windows" {
		mock = "jq() { command jq.exe -b \"$@\"; }\n" + mock
	}
	for _, test := range []struct {
		action, want string
	}{
		{"start-sandbox", "agentbox-fixed|Sandbox started"},
		{"stop-sandbox", "agentbox-fixed|Sandbox stopped"},
		{"restart-sandbox", "agentbox-fixed|Sandbox restarted"},
		{"delete-sandbox", "|Sandbox deleted"},
		{"configure-sandbox-proxy", "agentbox-fixed|沙箱网络代理已应用；新的 Agent 和终端进程将使用该代理"},
	} {
		t.Run(test.action, func(t *testing.T) {
			root := t.TempDir()
			job := writeExtensionTestJob(t, root, nil)
			encoded, err := os.ReadFile(job)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			jobObject := document["job"].(map[string]any)
			jobObject["id"] = "job-one"
			jobObject["leaseGeneration"] = 1
			jobObject["action"] = test.action
			encoded, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(job, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			result := filepath.Join(root, "completion.txt")
			command := exec.CommandContext(t.Context(), sh, "-s")
			command.Env = append(command.Environ(), "TEST_RESULT="+filepath.ToSlash(result), "TMPDIR="+filepath.ToSlash(root))
			command.Stdin = strings.NewReader("set -eu\n" + mock + functions + "\nprocess_job() {" + process + "\nprocess_job " + shellPath(job) + "\n")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("process job: %v\n%s", err, output)
			}
			completion, err := os.ReadFile(result)
			if err != nil || string(completion) != test.want ||
				strings.Contains(string(completion), "successful-guest-stdout-secret") {
				t.Fatalf("completion trusted operation stdout: %q, %v", completion, err)
			}
		})
	}
}

func TestExtensionsAreOnlyInstalledDuringCreation(t *testing.T) {
	_, create, found := strings.Cut(workerDaemon, "\ncreate_sandbox() {")
	if !found {
		t.Fatal("create_sandbox is missing")
	}
	create, lifecycle, found := strings.Cut(create, "\nsandbox_container_name() {")
	if !found {
		t.Fatal("sandbox lifecycle boundary is missing")
	}
	wrappers := strings.Index(create, `configure_sandbox_agent_config "$TARGET" "$JOB_FILE"`)
	extensions := strings.Index(create, `install_sandbox_extensions "$TARGET" "$JOB_FILE"`)
	setup := strings.Index(create, `if [ -n "$SETUP" ]`)
	if wrappers < 0 || extensions <= wrappers || setup <= extensions || strings.Contains(lifecycle, "install_sandbox_extensions") {
		t.Fatal("extensions must run once during creation after credentials/wrappers and before setup")
	}
	if !strings.Contains(workerDaemon, `\"sandbox-extensions\",\"mcp-managed-config\",\"managed-capability-config\",\"fail-closed-job-output\"`) {
		t.Fatal("Worker does not advertise sandbox extension execution")
	}
}
