package sessionworker

import (
	"strings"
	"testing"
)

func TestParseConfigBuildsWorkerWebSocketURL(t *testing.T) {
	config, err := parseConfig("https://agentbox.example:3000/\nserver-one\nsecret-token\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := config.websocketURL(); got != "wss://agentbox.example:3000/api/servers/server-one/sessions/connect" {
		t.Fatalf("websocket URL = %q", got)
	}
}

func TestValidPathNormalizesAbsoluteContainerPath(t *testing.T) {
	got, err := validPath("/workspace/project/../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspace/README.md" {
		t.Fatalf("path = %q", got)
	}
	for _, value := range []string{"", "relative.txt", "/tmp/\x00bad"} {
		if _, err := validPath(value); err == nil {
			t.Fatalf("path %q should be rejected", value)
		}
	}
}

func TestUploadTempPathRejectsInvalidID(t *testing.T) {
	const uploadID = "b5f68ee5-2e03-4af7-a065-385e58622496"
	got, err := uploadTempPath("/workspace/file.bin", uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspace/file.bin.agentbox-upload-"+uploadID {
		t.Fatalf("temporary upload path = %q", got)
	}
	if _, err := uploadTempPath("/workspace/file.bin", "../bad"); err == nil {
		t.Fatal("invalid upload ID should be rejected")
	}
}

func TestGoSessionWorkerKeepsUploadAndEnvironmentContracts(t *testing.T) {
	if maxFileSize != 512*1024 || maxUploadChunkSize != 192*1024 || maxUploadSize != 50*1024*1024 {
		t.Fatal("session file limits changed unexpectedly")
	}
	if !strings.Contains(loginShellCommand, `. /opt/agentbox/secrets/agentbox.env`) {
		t.Fatal("interactive terminal does not load sandbox environment variables")
	}
}
