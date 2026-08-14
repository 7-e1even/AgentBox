//go:build agentbox_driver

package main

import (
	"reflect"
	"testing"
)

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
