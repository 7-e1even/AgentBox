package store

import (
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestWorkerJobLeaseCoversLargeAgentImageBuilds(t *testing.T) {
	if workerJobLeaseDuration < 2*time.Hour {
		t.Fatalf("worker job lease = %s, want at least two hours", workerJobLeaseDuration)
	}
}

func TestRuntimeImageAvailabilityAllowsDockerPull(t *testing.T) {
	inventory := platform.ServerInventory{}
	if !runtimeImageIsAvailable("docker", inventory, "ubuntu:24.04", "amd64") {
		t.Fatal("Docker registry references should be pullable even when the local inventory is empty")
	}
	if runtimeImageIsAvailable("docker", inventory, "  ", "amd64") {
		t.Fatal("empty Docker image references must not be accepted")
	}
}

func TestRuntimeImageAvailabilityKeepsVMInventoryBound(t *testing.T) {
	inventory := platform.ServerInventory{VMImages: []platform.ServerImage{{
		Reference:    "ubuntu-24.04.qcow2",
		Architecture: "amd64",
		Path:         "/var/lib/agentbox/images/ubuntu-24.04.qcow2",
	}}}
	if !runtimeImageIsAvailable("vm", inventory, "/var/lib/agentbox/images/ubuntu-24.04.qcow2", "amd64") {
		t.Fatal("matching VM inventory image should be accepted")
	}
	if runtimeImageIsAvailable("vm", inventory, "missing.qcow2", "amd64") {
		t.Fatal("VM images must remain bound to the worker inventory")
	}
}
