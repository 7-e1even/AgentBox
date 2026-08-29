package store

import "testing"

func TestSelectSandboxAgentToolsKeepsRequestedSubset(t *testing.T) {
	selected := []string{"claude-code", "codex", "opencode"}
	tools, err := selectSandboxAgentTools(selected, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0] != "codex" {
		t.Fatalf("selected tools = %#v, want only codex", tools)
	}
}

func TestSelectSandboxAgentToolsUsesSelectedOrderForAll(t *testing.T) {
	selected := []string{"claude-code", "codex"}
	tools, err := selectSandboxAgentTools(selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0] != "claude-code" || tools[1] != "codex" {
		t.Fatalf("selected tools = %#v, want sandbox order", tools)
	}
}

func TestSelectSandboxAgentToolsRejectsUnknownTool(t *testing.T) {
	if _, err := selectSandboxAgentTools([]string{"codex"}, []string{"unknown"}); err == nil {
		t.Fatal("unknown Agent tool was accepted")
	}
}
