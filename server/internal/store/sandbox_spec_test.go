package store

import (
	"reflect"
	"strings"
	"testing"
)

func TestEffectiveSandboxSpecUsesTemplateDefaultsAndInstanceOverrides(t *testing.T) {
	runtimeSpec := map[string]any{
		"serverId":      "template-server",
		"driver":        "docker",
		"workdir":       "/workspace",
		"cpu":           "2",
		"memory":        "4 GiB",
		"desktop":       false,
		"credentialIds": []string{"template-key"},
		"environmentVariables": []any{
			map[string]any{"name": "NODE_ENV", "value": "development"},
		},
	}
	sandboxSpec := map[string]any{
		"serverId":      "sandbox-server",
		"driver":        "boxlite",
		"cpu":           "4",
		"desktop":       true,
		"credentialIds": []string{"sandbox-key"},
		"status":        "requested",
		"environmentVariables": []any{
			map[string]any{"name": "NODE_ENV", "value": "production"},
		},
	}

	effective := effectiveSandboxSpec(runtimeSpec, sandboxSpec)

	if got := effective["serverId"]; got != "sandbox-server" {
		t.Fatalf("serverId = %v, want sandbox-server", got)
	}
	if got := effective["driver"]; got != "boxlite" {
		t.Fatalf("driver = %v, want boxlite", got)
	}
	if got := effective["workdir"]; got != "/workspace" {
		t.Fatalf("workdir = %v, want template default", got)
	}
	if got := effective["memory"]; got != "4 GiB" {
		t.Fatalf("memory = %v, want template default", got)
	}
	if got := effective["cpu"]; got != "4" {
		t.Fatalf("cpu = %v, want sandbox override", got)
	}
	if got := effective["desktop"]; got != true {
		t.Fatalf("desktop = %v, want sandbox override", got)
	}
	if got := effective["credentialIds"]; !reflect.DeepEqual(got, []string{"sandbox-key"}) {
		t.Fatalf("credentialIds = %#v, want sandbox override", got)
	}
	if got := effective["environmentVariables"]; !reflect.DeepEqual(got, sandboxSpec["environmentVariables"]) {
		t.Fatalf("environmentVariables = %#v, want sandbox override", got)
	}
	if _, ok := effective["status"]; ok {
		t.Fatal("sandbox lifecycle metadata must not leak into the runtime spec")
	}
}

func TestEffectiveSandboxSpecAllowsClearingInheritedLists(t *testing.T) {
	runtimeSpec := map[string]any{
		"agentTools": []string{"codex", "claude-code"},
		"skillIds":   []string{"review"},
	}
	sandboxSpec := map[string]any{
		"agentTools": []string{},
		"skillIds":   []string{},
	}

	effective := effectiveSandboxSpec(runtimeSpec, sandboxSpec)

	if got := effective["agentTools"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("agentTools = %#v, want empty instance override", got)
	}
	if got := effective["skillIds"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("skillIds = %#v, want empty instance override", got)
	}
}

func TestIncompatibleAgentToolsChecksCredentialProtocols(t *testing.T) {
	protocols := map[string]bool{"anthropic": true}
	got := incompatibleAgentTools(
		[]string{"claude-code", "codex", "deepseek-harness", "grok", "kimi", "opencode", "pi", "reasonix"},
		protocols,
	)
	if len(got) != 0 {
		t.Fatalf("incompatibleAgentTools() = %#v, want none", got)
	}
}

func TestIncompatibleAgentToolsAllowsSingleConvertibleLLMProtocol(t *testing.T) {
	for _, protocol := range []string{"anthropic", "openai-responses", "openai-chat"} {
		got := incompatibleAgentTools(
			[]string{"claude-code", "codex", "deepseek-harness", "grok", "kimi", "opencode", "pi", "reasonix"},
			map[string]bool{protocol: true},
		)
		if len(got) != 0 {
			t.Fatalf("protocol %s incompatibleAgentTools() = %#v, want none", protocol, got)
		}
	}
}

func TestDeepSeekHarnessAcceptsGeminiThroughChatFacade(t *testing.T) {
	got := incompatibleAgentTools(
		[]string{"deepseek-harness"},
		map[string]bool{"gemini": true},
	)
	if len(got) != 0 {
		t.Fatalf("incompatibleAgentTools() = %#v, want none", got)
	}
}

func TestGrokBuildUsesResponsesFacade(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "anthropic", "openai-chat"} {
		if got := incompatibleAgentTools([]string{"grok"}, map[string]bool{protocol: true}); len(got) != 0 {
			t.Fatalf("protocol %s incompatibleAgentTools() = %#v, want none", protocol, got)
		}
	}
}

func TestSandboxUpdatesPreserveWorkerManagedLifecycleFields(t *testing.T) {
	for _, expected := range []string{
		`- 'proxyId' - 'appliedProxyId' - 'proxyOperation'`,
		`'status', spec->'status'`,
		`'message', spec->'message'`,
		`'externalId', spec->'externalId'`,
		`'provisioning', spec->'provisioning'`,
		`'proxyId', COALESCE(spec->'proxyId'`,
		`'appliedProxyId', spec->'appliedProxyId'`,
		`'proxyOperation', spec->'proxyOperation'`,
	} {
		if !strings.Contains(resourceUpdateSpecSQL, expected) {
			t.Fatalf("sandbox update does not preserve Worker-managed field %q", expected)
		}
	}
}
