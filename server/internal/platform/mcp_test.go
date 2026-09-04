package platform

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalizeMCPCompatibilityInput(t *testing.T) {
	input := Input{Kind: KindMCP, Spec: map[string]any{
		"transport": " STDIO ",
		"command":   " npx ",
		"args":      "-y example",
	}}
	if err := CanonicalizeResourceSpec(&input); err != nil {
		t.Fatal(err)
	}
	if got := stringList(input.Spec["args"]); !reflect.DeepEqual(got, []string{"-y", "example"}) {
		t.Fatalf("canonical args = %#v", got)
	}
	if input.Spec["transport"] != "stdio" || input.Spec["command"] != "npx" {
		t.Fatalf("canonical spec = %#v", input.Spec)
	}

	input = Input{Kind: KindMCP, Spec: map[string]any{
		"transport": "http",
		"url":       "https://mcp.example.test/rpc",
		"headers":   "authorization=secret://MCP_TOKEN\nX-Tenant=env://TENANT",
	}}
	if err := CanonicalizeResourceSpec(&input); err != nil {
		t.Fatal(err)
	}
	headers, ok := input.Spec["headers"].([]any)
	if !ok || len(headers) != 2 || headers[0].(map[string]any)["name"] != "Authorization" {
		t.Fatalf("canonical headers = %#v", input.Spec["headers"])
	}
}

func TestMCPRejectsAmbiguousLegacyAndUnsafeHeaders(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"quoted legacy args": {
			"transport": "stdio", "command": "npx", "args": `--label "two words"`,
		},
		"literal header": {
			"transport": "http", "url": "https://mcp.example.test", "headers": "Authorization=Bearer plaintext",
		},
		"duplicate header": {
			"transport": "http", "url": "https://mcp.example.test", "headers": []any{
				map[string]any{"name": "X-Key", "valueFrom": "env://FIRST"},
				map[string]any{"name": "x-key", "valueFrom": "env://SECOND"},
			},
		},
		"reserved header": {
			"transport": "http", "url": "https://mcp.example.test", "headers": []any{
				map[string]any{"name": "Mcp-Session-Id", "valueFrom": "env://SESSION"},
			},
		},
		"stdio header": {
			"transport": "stdio", "command": "mcp", "headers": "X-Key=env://KEY",
		},
		"URL user info": {
			"transport": "http", "url": "https://user:password@mcp.example.test",
		},
		"URL fragment": {
			"transport": "http", "url": "https://mcp.example.test/#secret",
		},
		"URL query": {
			"transport": "http", "url": "https://mcp.example.test/?token=secret",
		},
		"URL empty query": {
			"transport": "http", "url": "https://mcp.example.test/path?",
		},
		"command newline": {
			"transport": "stdio", "command": "mcp\nother",
		},
		"URL too long": {
			"transport": "http", "url": "https://mcp.example.test/" + strings.Repeat("a", MaxMCPURLRunes),
		},
		"header name too long": {
			"transport": "http", "url": "https://mcp.example.test", "headers": []any{
				map[string]any{"name": strings.Repeat("X", MaxMCPHeaderNameLength+1), "valueFrom": "env://TOKEN"},
			},
		},
		"header reference too long": {
			"transport": "http", "url": "https://mcp.example.test", "headers": []any{
				map[string]any{"name": "X-Key", "valueFrom": "env://" + strings.Repeat("A", MaxVariableNameLength+1)},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := Input{Kind: KindMCP, Spec: spec}
			if err := CanonicalizeResourceSpec(&input); err == nil {
				err = ValidateMCPSpec(*mustDecodeMCP(t, input.Spec))
				if err == nil {
					t.Fatal("unsafe MCP configuration was accepted")
				}
			}
		})
	}
}

func TestCanonicalizeVariableCompatibilityInput(t *testing.T) {
	input := Input{Kind: KindVariable, Spec: map[string]any{
		"key": " TOKEN ", "reference": " secret://SOURCE_TOKEN ",
	}}
	if err := CanonicalizeResourceSpec(&input); err != nil {
		t.Fatal(err)
	}
	if input.Spec["key"] != "TOKEN" || input.Spec["mode"] != "secret-ref" || input.Spec["reference"] != "secret://SOURCE_TOKEN" {
		t.Fatalf("canonical Variable spec = %#v", input.Spec)
	}
	decoded, err := DecodeResourceSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVariableSpec(*decoded.(*VariableSpec)); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalMCPArgsPreserveWhitespace(t *testing.T) {
	spec := map[string]any{
		"transport": "stdio", "command": "mcp", "args": []any{"--label", "two words"},
	}
	input := Input{Kind: KindMCP, Spec: spec}
	if err := CanonicalizeResourceSpec(&input); err != nil {
		t.Fatal(err)
	}
	decoded := mustDecodeMCP(t, input.Spec)
	if err := ValidateMCPSpec(*decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Args, []string{"--label", "two words"}) {
		t.Fatalf("args = %#v", decoded.Args)
	}
}

func TestMCPRawValuesAllowJSONWhitespace(t *testing.T) {
	var spec MCPSpec
	if err := json.Unmarshal([]byte("{\"transport\":\"stdio\",\"command\":\"mcp\",\"args\": \n [\"one\"],\"headers\": \n []}"), &spec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Args, []string{"one"}) {
		t.Fatalf("args = %#v", spec.Args)
	}
}

func TestMCPReadSpecNeverReturnsLiteralLegacyHeader(t *testing.T) {
	spec := MCPReadSpec(map[string]any{
		"transport": "http", "url": "https://mcp.example.test", "headers": "Authorization=Bearer plaintext",
	})
	if _, exists := spec["headers"]; exists {
		t.Fatalf("literal header returned: %#v", spec["headers"])
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.TrimSpace(string(encoded)), "plaintext") {
		t.Fatal("plaintext remained in MCP read shape")
	}
}

func TestMCPReadSpecNeverReturnsUnsafeLegacyURL(t *testing.T) {
	for name, rawURL := range map[string]string{
		"userinfo":    "https://user:read-url-plaintext-secret@mcp.example.test/rpc",
		"query":       "https://mcp.example.test/rpc?token=read-url-plaintext-secret",
		"empty query": "https://mcp.example.test/rpc?",
		"fragment":    "https://mcp.example.test/rpc#read-url-plaintext-secret",
	} {
		t.Run(name, func(t *testing.T) {
			spec := MCPReadSpec(map[string]any{"transport": "http", "url": rawURL})
			if _, exists := spec["url"]; exists {
				t.Fatalf("unsafe URL returned: %#v", spec["url"])
			}
			encoded, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "read-url-plaintext-secret") {
				t.Fatal("unsafe URL contents remained in MCP read shape")
			}
		})
	}

	spec := MCPReadSpec(map[string]any{
		"transport": "http", "url": " https://mcp.example.test/rpc ",
	})
	if spec["url"] != "https://mcp.example.test/rpc" {
		t.Fatalf("safe URL = %#v", spec["url"])
	}

	fallback := MCPReadSpec(map[string]any{
		"transport": "http",
		"url":       "https://user:fallback-plaintext-secret@mcp.example.test/rpc",
		"headers":   map[string]any{"X-Key": "fallback-plaintext-secret"},
	})
	encoded, err := json.Marshal(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fallback-plaintext-secret") {
		t.Fatalf("fallback read shape retained unsafe data: %s", encoded)
	}
}

func mustDecodeMCP(t *testing.T, spec map[string]any) *MCPSpec {
	t.Helper()
	decoded, err := DecodeResourceSpec(Input{Kind: KindMCP, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(*MCPSpec)
}
