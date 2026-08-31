package store

import "testing"

func TestWorkerJobAttemptLimitOnlyRetriesExplicitlyIdempotentChecks(t *testing.T) {
	for _, action := range []string{"check-network-proxy", "check-sandbox-agent-tools"} {
		if got := workerJobAttemptLimit(action); got != workerJobAutomaticRetryLimit {
			t.Errorf("workerJobAttemptLimit(%q) = %d, want %d", action, got, workerJobAutomaticRetryLimit)
		}
	}

	for _, action := range []string{
		"create-sandbox",
		"start-sandbox",
		"stop-sandbox",
		"restart-sandbox",
		"delete-sandbox",
		"configure-sandbox-proxy",
		"update-sandbox-agent-tools",
		"update-worker",
	} {
		if got := workerJobAttemptLimit(action); got != 1 {
			t.Errorf("workerJobAttemptLimit(%q) = %d, want 1", action, got)
		}
	}
}
