//go:build agentbox_driver

package main

import (
	"reflect"
	"testing"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func TestParseImagesCommand(t *testing.T) {
	parsed, err := parseCommand([]string{"microsandbox", "images"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.action != "images" {
		t.Fatalf("action = %q, want images", parsed.action)
	}
}

func TestParsePrepareImageWithOCIArchive(t *testing.T) {
	parsed, err := parseCommand([]string{
		"prepare-image", "alpine:3.20", "--archive", "/var/lib/agentbox/oci-image.tar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.image != "alpine:3.20" || parsed.path != "/var/lib/agentbox/oci-image.tar" {
		t.Fatalf("unexpected prepare-image command: %#v", parsed)
	}
}

func TestFormatImageSize(t *testing.T) {
	bytes := int64(1572864)
	if got := formatImageSize(&bytes); got != "1.5 MiB" {
		t.Fatalf("formatImageSize() = %q, want 1.5 MiB", got)
	}
	if got := formatImageSize(nil); got != "" {
		t.Fatalf("formatImageSize(nil) = %q, want empty", got)
	}
}

func TestParseCreateCommand(t *testing.T) {
	parsed, err := parseCommand([]string{
		"microsandbox", "create", "agentbox-test", "ubuntu:24.04",
		"--cpus", "4", "--memory", "8192", "--workdir", "/workspace", "--network", "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.action != "create" || parsed.target != "agentbox-test" || parsed.image != "ubuntu:24.04" {
		t.Fatalf("unexpected create command: %#v", parsed)
	}
	if parsed.cpus != 4 || parsed.memory != 8192 || parsed.workdir != "/workspace" || parsed.network != "none" {
		t.Fatalf("unexpected create options: %#v", parsed)
	}
}

func TestParseCreateCommandDefaultsToNone(t *testing.T) {
	parsed, err := parseCommand([]string{"create", "agentbox-test", "ubuntu:24.04"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.network != "none" {
		t.Fatalf("default network = %q, want none", parsed.network)
	}
}

func TestParseCreateCommandRejectsUnsupportedNetworkPolicies(t *testing.T) {
	for _, network := range []string{"restricted", "unexpected"} {
		t.Run(network, func(t *testing.T) {
			_, err := parseCommand([]string{
				"create", "agentbox-test", "ubuntu:24.04", "--network", network,
			})
			if err == nil {
				t.Fatalf("network policy %q was accepted", network)
			}
		})
	}
}

func TestMicrosandboxNetworkPolicyMatchesAgentBoxSemantics(t *testing.T) {
	if policy := microsandboxNetworkPolicy("none"); policy.DefaultEgress != microsandbox.PolicyActionDeny {
		t.Fatalf("none default egress = %q, want deny", policy.DefaultEgress)
	}
	policy := microsandboxNetworkPolicy("egress")
	destinations := make(map[string]bool, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.Action == microsandbox.PolicyActionAllow && rule.Direction == microsandbox.PolicyDirectionEgress {
			destinations[rule.Destination] = true
		}
	}
	for _, destination := range []string{"public", "private", "host"} {
		if !destinations[destination] {
			t.Fatalf("egress policy does not allow %s destinations: %#v", destination, policy.Rules)
		}
	}
}

func TestValidateMicrosandboxLabels(t *testing.T) {
	needsAdoption, err := validateMicrosandboxLabels(
		map[string]string{"agentbox.sandbox": "sandbox-one"},
		"agentbox-sandbox-one",
	)
	if err != nil || needsAdoption {
		t.Fatalf("validateMicrosandboxLabels() rejected owned sandbox: %v", err)
	}
	needsAdoption, err = validateMicrosandboxLabels(nil, "agentbox-sandbox-one")
	if err != nil || !needsAdoption {
		t.Fatalf("legacy labels adoption = %v, error = %v", needsAdoption, err)
	}
	for _, labels := range []map[string]string{
		{"agentbox.sandbox": "another-sandbox"},
		{"agentbox.sandbox": ""},
	} {
		if _, err := validateMicrosandboxLabels(labels, "agentbox-sandbox-one"); err == nil {
			t.Fatalf("validateMicrosandboxLabels(%v) accepted mismatched ownership", labels)
		}
	}
}

func TestExpectedMicrosandboxOwnerRequiresExactManagedTarget(t *testing.T) {
	owner, err := expectedMicrosandboxOwner("agentbox-sandbox-one", "agentbox-sandbox-one")
	if err != nil || owner != "sandbox-one" {
		t.Fatalf("expectedMicrosandboxOwner() = %q, %v", owner, err)
	}
	for _, test := range []struct {
		handleName string
		target     string
	}{
		{handleName: "agentbox-other", target: "agentbox-sandbox-one"},
		{handleName: "sandbox-one", target: "sandbox-one"},
		{handleName: "agentbox-A", target: "agentbox-A"},
		{handleName: "agentbox-a--b", target: "agentbox-a--b"},
		{handleName: "agentbox-a", target: "agentbox-a"},
	} {
		if _, err := expectedMicrosandboxOwner(test.handleName, test.target); err == nil {
			t.Fatalf("expectedMicrosandboxOwner(%q, %q) accepted invalid target", test.handleName, test.target)
		}
	}
}

func TestParseExecCommand(t *testing.T) {
	parsed, err := parseCommand([]string{
		"exec", "--stdin", "--workdir", "/tmp", "agentbox-test", "--", "sh", "-c", "cat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.stdin || parsed.target != "agentbox-test" || parsed.workdir != "/tmp" {
		t.Fatalf("unexpected exec options: %#v", parsed)
	}
	if !reflect.DeepEqual(parsed.command, []string{"sh", "-c", "cat"}) {
		t.Fatalf("command = %#v", parsed.command)
	}
}

func TestParseFilesystemCommands(t *testing.T) {
	parsed, err := parseCommand([]string{"fs-write", "agentbox-test", "/workspace/file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.target != "agentbox-test" || parsed.path != "/workspace/file.txt" {
		t.Fatalf("unexpected filesystem command: %#v", parsed)
	}

	renamed, err := parseCommand([]string{"fs-rename", "agentbox-test", "/tmp/source", "/workspace/final"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.path != "/tmp/source" || renamed.dest != "/workspace/final" {
		t.Fatalf("unexpected rename command: %#v", renamed)
	}
}
