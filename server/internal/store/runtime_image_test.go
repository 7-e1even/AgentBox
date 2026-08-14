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

func TestRuntimeImageAvailabilityAllowsOCIPullForManagedDrivers(t *testing.T) {
	inventory := platform.ServerInventory{}
	for _, driver := range []string{"docker", "boxlite", "microsandbox"} {
		if !runtimeImageIsAvailable(driver, inventory, "ubuntu:24.04", "amd64") {
			t.Fatalf("%s registry references should be pullable when local inventory is empty", driver)
		}
		if runtimeImageIsAvailable(driver, inventory, "  ", "amd64") {
			t.Fatalf("empty %s image references must not be accepted", driver)
		}
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
