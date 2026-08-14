package store

import (
	"reflect"
	"testing"
)

func TestEffectiveSandboxSpecUsesTemplateDefaultsAndInstanceOverrides(t *testing.T) {
	runtimeSpec := map[string]any{
		"serverId":      "template-server",
		"driver":        "docker",
		"workdir":       "/workspace",
		"cpu":           "2",
		"memory":        "4 GiB",
		"credentialIds": []string{"template-key"},
	}
	sandboxSpec := map[string]any{
		"serverId":      "sandbox-server",
		"driver":        "boxlite",
		"cpu":           "4",
		"credentialIds": []string{"sandbox-key"},
		"status":        "requested",
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
	if got := effective["credentialIds"]; !reflect.DeepEqual(got, []string{"sandbox-key"}) {
		t.Fatalf("credentialIds = %#v, want sandbox override", got)
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
