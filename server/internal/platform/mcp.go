package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxMCPArguments        = 128
	MaxMCPURLRunes         = 8192
	MaxMCPHeaderNameLength = 128
	MaxVariableNameLength  = 128
)

var mcpReferencePattern = regexp.MustCompile(`^(env|secret)://([A-Za-z_][A-Za-z0-9_]*)$`)
var mcpHeaderNamePattern = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

var reservedMCPHeaders = map[string]bool{
	"accept":               true,
	"connection":           true,
	"content-length":       true,
	"content-type":         true,
	"host":                 true,
	"keep-alive":           true,
	"last-event-id":        true,
	"mcp-method":           true,
	"mcp-name":             true,
	"mcp-protocol-version": true,
	"mcp-session-id":       true,
	"proxy-authenticate":   true,
	"proxy-authorization":  true,
	"te":                   true,
	"trailer":              true,
	"transfer-encoding":    true,
	"upgrade":              true,
}

// UnmarshalJSON accepts the original textarea representation at the API
// boundary, while every successful write is re-encoded to the canonical
// arrays by CanonicalizeResourceSpec.
func (spec *MCPSpec) UnmarshalJSON(data []byte) error {
	type rawSpec struct {
		Transport string          `json:"transport"`
		Command   string          `json:"command,omitempty"`
		Args      json.RawMessage `json:"args,omitempty"`
		URL       string          `json:"url,omitempty"`
		Headers   json.RawMessage `json:"headers,omitempty"`
		Cwd       string          `json:"cwd,omitempty"`
	}
	var raw rawSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	args, err := decodeMCPArguments(raw.Args)
	if err != nil {
		return err
	}
	headers, err := decodeMCPHeaders(raw.Headers)
	if err != nil {
		return err
	}
	*spec = MCPSpec{
		Transport: raw.Transport,
		Command:   raw.Command,
		Args:      args,
		URL:       raw.URL,
		Headers:   headers,
		Cwd:       raw.Cwd,
	}
	return nil
}

func decodeMCPArguments(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var args []string
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("args must be a string array: %w", err)
		}
		return args, nil
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, fmt.Errorf("args must be a string array or legacy string: %w", err)
	}
	if strings.ContainsAny(legacy, "'\"\\") {
		return nil, fmt.Errorf("legacy args containing quotes or escapes are ambiguous; submit args as a string array")
	}
	return slices.Collect(strings.FieldsSeq(legacy)), nil
}

func decodeMCPHeaders(raw json.RawMessage) ([]MCPHeader, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("headers must be an array: %w", err)
		}
		headers := make([]MCPHeader, 0, len(entries))
		for _, entry := range entries {
			var header MCPHeader
			decoder := json.NewDecoder(bytes.NewReader(entry))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&header); err != nil {
				return nil, fmt.Errorf("invalid MCP header: %w", err)
			}
			headers = append(headers, header)
		}
		return headers, nil
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, fmt.Errorf("headers must be an array or legacy string: %w", err)
	}
	if strings.TrimSpace(legacy) == "" {
		return nil, nil
	}
	lines := strings.Split(strings.ReplaceAll(legacy, "\r\n", "\n"), "\n")
	headers := make([]MCPHeader, 0, len(lines))
	for _, line := range lines {
		name, valueFrom, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			return nil, fmt.Errorf("legacy MCP headers must use Name=scheme://KEY")
		}
		headers = append(headers, MCPHeader{Name: strings.TrimSpace(name), ValueFrom: strings.TrimSpace(valueFrom)})
	}
	return headers, nil
}

func ValidateMCPSpec(spec MCPSpec) error {
	spec.Transport = strings.ToLower(strings.TrimSpace(spec.Transport))
	switch spec.Transport {
	case "stdio":
		if strings.TrimSpace(spec.Command) == "" {
			return &ValidationError{Message: "stdio MCP 需要启动命令"}
		}
		if !utf8.ValidString(spec.Command) || utf8.RuneCountInString(spec.Command) > 4096 || strings.ContainsAny(spec.Command, "\x00\r\n") {
			return &ValidationError{Message: "stdio MCP command 无效"}
		}
		if strings.TrimSpace(spec.URL) != "" {
			return &ValidationError{Message: "stdio MCP 不能同时配置 URL"}
		}
		if len(spec.Headers) > 0 {
			return &ValidationError{Message: "stdio MCP 不能配置 HTTP Header"}
		}
	case "http":
		if strings.TrimSpace(spec.Command) != "" || len(spec.Args) > 0 || strings.TrimSpace(spec.Cwd) != "" {
			return &ValidationError{Message: "HTTP MCP 不能配置 command、args 或 cwd"}
		}
		if utf8.RuneCountInString(spec.URL) > MaxMCPURLRunes {
			return &ValidationError{Message: "HTTP MCP URL 不能超过 8192 个字符"}
		}
		if !safeMCPHTTPURL(spec.URL) {
			return &ValidationError{Message: "HTTP MCP URL 必须是无凭据、query 和 fragment 的 HTTP(S) 地址"}
		}
	default:
		return &ValidationError{Message: "MCP transport 只能是 stdio 或 http"}
	}
	if len(spec.Args) > MaxMCPArguments {
		return &ValidationError{Message: "MCP 参数不能超过 128 个"}
	}
	for _, arg := range spec.Args {
		if arg == "" || utf8.RuneCountInString(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return &ValidationError{Message: "MCP 参数无效"}
		}
	}
	if spec.Cwd != "" && (len(spec.Cwd) > 4096 || !strings.HasPrefix(spec.Cwd, "/") || strings.ContainsAny(spec.Cwd, "\x00\r\n")) {
		return &ValidationError{Message: "MCP cwd 必须是有效的绝对路径"}
	}
	if len(spec.Headers) > 64 {
		return &ValidationError{Message: "MCP Header 不能超过 64 个"}
	}
	seen := make(map[string]struct{}, len(spec.Headers))
	for _, header := range spec.Headers {
		name := strings.TrimSpace(header.Name)
		if utf8.RuneCountInString(name) > MaxMCPHeaderNameLength || !mcpHeaderNamePattern.MatchString(name) {
			return &ValidationError{Message: "MCP Header 名称无效"}
		}
		key := strings.ToLower(name)
		if reservedMCPHeaders[key] {
			return &ValidationError{Message: "MCP Header 不能覆盖连接或协议保留字段"}
		}
		if _, duplicate := seen[key]; duplicate {
			return &ValidationError{Message: "MCP Header 名称不能重复"}
		}
		seen[key] = struct{}{}
		if _, key, ok := ParseMCPValueReference(header.ValueFrom); !ok || len(key) > MaxVariableNameLength {
			return &ValidationError{Message: "MCP Header valueFrom 只能引用 env://KEY 或 secret://KEY，不能保存明文"}
		}
	}
	return nil
}

func safeMCPHTTPURL(raw string) bool {
	link, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (link.Scheme == "http" || link.Scheme == "https") && link.Host != "" &&
		link.User == nil && link.RawQuery == "" && !link.ForceQuery && link.Fragment == ""
}

func ParseMCPValueReference(value string) (scheme, key string, ok bool) {
	parts := mcpReferencePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func MCPURLHost(spec MCPSpec) string {
	if spec.Transport != "http" {
		return ""
	}
	link, err := url.Parse(spec.URL)
	if err != nil {
		return ""
	}
	return link.Hostname()
}

func IsLoopbackMCPHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// MCPReadSpec converts readable compatibility fields but never reflects a
// literal or malformed header value from legacy storage.
func MCPReadSpec(raw map[string]any) map[string]any {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return safeMCPReadFallback(raw)
	}
	var spec MCPSpec
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return safeMCPReadFallback(raw)
	}
	spec.URL = strings.TrimSpace(spec.URL)
	if spec.URL != "" && !safeMCPHTTPURL(spec.URL) {
		spec.URL = ""
	}
	for _, header := range spec.Headers {
		if !mcpHeaderNamePattern.MatchString(header.Name) || reservedMCPHeaders[strings.ToLower(header.Name)] {
			spec.Headers = nil
			break
		}
		if _, _, ok := ParseMCPValueReference(header.ValueFrom); !ok {
			spec.Headers = nil
			break
		}
	}
	encoded, err = json.Marshal(spec)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func safeMCPReadFallback(raw map[string]any) map[string]any {
	result := maps.Clone(raw)
	delete(result, "headers")
	urlValue, exists := result["url"]
	if !exists {
		return result
	}
	rawURL, ok := urlValue.(string)
	if !ok || !safeMCPHTTPURL(rawURL) {
		delete(result, "url")
		return result
	}
	result["url"] = strings.TrimSpace(rawURL)
	return result
}
