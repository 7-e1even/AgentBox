//go:build agentbox_driver

package main

import (
	"reflect"
	"testing"
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
