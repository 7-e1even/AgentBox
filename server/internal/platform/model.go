package platform

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Kind string

const (
	KindProject  Kind = "project"
	KindImage    Kind = "image"
	KindRuntime  Kind = "runtime"
	KindSkill    Kind = "skill"
	KindMCP      Kind = "mcp"
	KindSandbox  Kind = "sandbox"
	KindVariable Kind = "variable"
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var allowedAgentTools = map[string]bool{
	"claude-code":      true,
	"codex":            true,
	"deepseek-harness": true,
	"gemini-cli":       true,
	"grok":             true,
	"kimi":             true,
	"opencode":         true,
	"pi":               true,
	"reasonix":         true,
}

var environmentVariableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Input struct {
	ID          string         `json:"id"`
	Kind        Kind           `json:"kind"`
	ProjectID   *string        `json:"projectId"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Enabled     bool           `json:"enabled"`
	Spec        map[string]any `json:"spec"`
}

type Resource struct {
	Input
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Snapshot struct {
	Resources []Resource `json:"resources"`
}

type ManagedServer struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	Hostname            string          `json:"hostname"`
	OS                  string          `json:"os"`
	Arch                string          `json:"arch"`
	Capabilities        []string        `json:"capabilities"`
	Inventory           ServerInventory `json:"inventory"`
	WorkerVersion       string          `json:"workerVersion"`
	WorkerUpdateStatus  string          `json:"workerUpdateStatus"`
	WorkerUpdateTarget  string          `json:"workerUpdateTarget"`
	WorkerUpdateMessage string          `json:"workerUpdateMessage"`
	Status              string          `json:"status"`
	LastSeenAt          time.Time       `json:"lastSeenAt"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

type ServerImage struct {
	ID           string `json:"id"`
	Reference    string `json:"reference"`
	Architecture string `json:"architecture"`
	Size         string `json:"size"`
	Created      string `json:"created"`
	Format       string `json:"format"`
	Path         string `json:"path"`
	Source       string `json:"source"`
}

type ServerInventory struct {
	DockerImages       []ServerImage `json:"dockerImages"`
	BoxLiteImages      []ServerImage `json:"boxliteImages"`
	MicrosandboxImages []ServerImage `json:"microsandboxImages"`
	VMImages           []ServerImage `json:"vmImages"`
	VMImageDirectory   string        `json:"vmImageDirectory"`
}

type ServerPairing struct {
	ID        string     `json:"id"`
	Token     string     `json:"token,omitempty"`
	ExpiresAt time.Time  `json:"expiresAt"`
	ServerID  *string    `json:"serverId"`
	ClaimedAt *time.Time `json:"claimedAt"`
}

type ServerRegistration struct {
	PairingToken string   `json:"pairingToken"`
	ServerID     string   `json:"serverId"`
	Name         string   `json:"name"`
	Hostname     string   `json:"hostname"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	Capabilities []string `json:"capabilities"`
	// PreviousCredential is required when re-registering a known serverId:
	// it must match the server's current credential to authorize rotation.
	PreviousCredential string `json:"previousCredential"`
}

type CredentialInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"providerId"`
	Protocol   string `json:"protocol"`
	Endpoint   string `json:"endpoint"`
	ModelID    string `json:"modelId"`
	Secret     string `json:"secret"`
	Enabled    bool   `json:"enabled"`
}

type ManagedCredential struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ProviderID     string            `json:"providerId"`
	Protocol       string            `json:"protocol"`
	Endpoint       string            `json:"endpoint"`
	ModelID        string            `json:"modelId"`
	Models         []CredentialModel `json:"models"`
	MaskedSecret   string            `json:"maskedSecret"`
	Enabled        bool              `json:"enabled"`
	LastCheckAt    *time.Time        `json:"lastCheckAt"`
	LastCheckOK    *bool             `json:"lastCheckOk"`
	LastCheckError string            `json:"lastCheckError"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type NetworkProxyInput struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Scheme   string   `json:"scheme"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	NoProxy  []string `json:"noProxy"`
	Enabled  bool     `json:"enabled"`
}

type ManagedNetworkProxy struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Scheme         string    `json:"scheme"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	Username       string    `json:"username"`
	MaskedPassword string    `json:"maskedPassword"`
	HasPassword    bool      `json:"hasPassword"`
	NoProxy        []string  `json:"noProxy"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CredentialModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Source string `json:"source"`
}

type CredentialModelInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RuntimeLLMTarget is the server-side upstream bound to one sandbox credential.
// Secret is intentionally never serialized into worker jobs or API responses.
type RuntimeLLMTarget struct {
	SandboxID    string
	CredentialID string
	ProviderID   string
	Protocol     string
	Endpoint     string
	ModelID      string
	Secret       string
}

type WorkerJob struct {
	ID         string         `json:"id"`
	ResourceID string         `json:"resourceId"`
	Action     string         `json:"action"`
	Payload    map[string]any `json:"payload"`
}

type WorkerJobResult struct {
	Success         bool            `json:"success"`
	ExternalID      string          `json:"externalId"`
	Message         string          `json:"message"`
	ExitCode        *int            `json:"exitCode,omitempty"`
	Output          string          `json:"output,omitempty"`
	OutputTruncated bool            `json:"outputTruncated,omitempty"`
	TimedOut        bool            `json:"timedOut,omitempty"`
	Error           *WorkerJobError `json:"error,omitempty"`
}

type WorkerJobError struct {
	Code      string            `json:"code"`
	Stage     string            `json:"stage,omitempty"`
	Retryable bool              `json:"retryable"`
	Details   map[string]string `json:"details,omitempty"`
}

type WorkerJobProgressInput struct {
	Stage           string `json:"stage"`
	Message         string `json:"message"`
	CacheStatus     string `json:"cacheStatus,omitempty"`
	CacheReason     string `json:"cacheReason,omitempty"`
	AgentTool       string `json:"agentTool,omitempty"`
	AgentToolStatus string `json:"agentToolStatus,omitempty"`
}

type ProvisioningStageTiming struct {
	Stage      string    `json:"stage"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	DurationMS int64     `json:"durationMs"`
}

type ProvisioningAgentTool struct {
	Tool       string     `json:"tool"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	DurationMS int64      `json:"durationMs,omitempty"`
}

type ProvisioningProgress struct {
	Stage          string                    `json:"stage,omitempty"`
	Message        string                    `json:"message,omitempty"`
	Status         string                    `json:"status,omitempty"`
	CacheStatus    string                    `json:"cacheStatus,omitempty"`
	CacheReason    string                    `json:"cacheReason,omitempty"`
	StartedAt      *time.Time                `json:"startedAt,omitempty"`
	StageStartedAt *time.Time                `json:"stageStartedAt,omitempty"`
	UpdatedAt      *time.Time                `json:"updatedAt,omitempty"`
	FinishedAt     *time.Time                `json:"finishedAt,omitempty"`
	DurationMS     int64                     `json:"durationMs,omitempty"`
	Timings        []ProvisioningStageTiming `json:"timings,omitempty"`
	AgentTools     []ProvisioningAgentTool   `json:"agentTools,omitempty"`
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func Normalize(input *Input) {
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.ProjectID != nil {
		value := strings.TrimSpace(*input.ProjectID)
		if value == "" {
			input.ProjectID = nil
		} else {
			input.ProjectID = &value
		}
	}
	if input.Spec == nil {
		input.Spec = map[string]any{}
	}
	if input.Kind == KindRuntime || input.Kind == KindSandbox {
		input.Spec["environmentVariables"] = SandboxEnvironmentVariables(
			input.Spec["environmentVariables"],
		)
	}
}

func SandboxEnvironmentVariables(value any) []any {
	variables, _ := value.([]any)
	result := make([]any, 0, len(variables)+1)
	for _, variable := range variables {
		entry, ok := variable.(map[string]any)
		if ok {
			name, _ := entry["name"].(string)
			if name == "IS_SANDBOX" {
				continue
			}
		}
		result = append(result, variable)
	}
	return append(result, map[string]any{"name": "IS_SANDBOX", "value": "1"})
}

func NormalizeServerRegistration(input *ServerRegistration) {
	input.PairingToken = strings.TrimSpace(input.PairingToken)
	input.ServerID = strings.TrimSpace(input.ServerID)
	input.Name = strings.TrimSpace(input.Name)
	input.Hostname = strings.TrimSpace(input.Hostname)
	input.OS = strings.ToLower(strings.TrimSpace(input.OS))
	input.Arch = strings.ToLower(strings.TrimSpace(input.Arch))
	if input.Name == "" {
		input.Name = input.Hostname
	}
	if input.Capabilities == nil {
		input.Capabilities = []string{}
	}
}

func ValidateServerRegistration(input ServerRegistration) error {
	if len(input.PairingToken) < 32 {
		return &ValidationError{Message: "配对令牌无效"}
	}
	if _, err := uuid.Parse(input.ServerID); err != nil {
		return &ValidationError{Message: "服务器标识无效"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 1 || n > 80 {
		return &ValidationError{Message: "服务器名称需要 1 到 80 个字符"}
	}
	if n := utf8.RuneCountInString(input.Hostname); n < 1 || n > 255 {
		return &ValidationError{Message: "服务器主机名无效"}
	}
	if input.OS != "linux" {
		return &ValidationError{Message: "当前仅支持 Linux 服务器"}
	}
	if input.Arch != "amd64" && input.Arch != "arm64" {
		return &ValidationError{Message: "当前仅支持 amd64 或 arm64 架构"}
	}
	if len(input.Capabilities) > 32 {
		return &ValidationError{Message: "服务器能力数量过多"}
	}
	for _, capability := range input.Capabilities {
		if n := utf8.RuneCountInString(capability); n < 1 || n > 64 {
			return &ValidationError{Message: "服务器能力无效"}
		}
	}
	return nil
}

func NormalizeServerInventory(inventory *ServerInventory) {
	if inventory.DockerImages == nil {
		inventory.DockerImages = []ServerImage{}
	}
	if inventory.BoxLiteImages == nil {
		inventory.BoxLiteImages = []ServerImage{}
	}
	if inventory.MicrosandboxImages == nil {
		inventory.MicrosandboxImages = []ServerImage{}
	}
	if inventory.VMImages == nil {
		inventory.VMImages = []ServerImage{}
	}
	inventory.VMImageDirectory = strings.TrimSpace(inventory.VMImageDirectory)
}

func ValidateServerInventory(inventory ServerInventory) error {
	if len(inventory.DockerImages) > 2000 || len(inventory.BoxLiteImages) > 2000 ||
		len(inventory.MicrosandboxImages) > 2000 || len(inventory.VMImages) > 2000 {
		return &ValidationError{Message: "服务器镜像数量过多"}
	}
	if utf8.RuneCountInString(inventory.VMImageDirectory) > 500 {
		return &ValidationError{Message: "VM 镜像目录过长"}
	}
	images := append([]ServerImage{}, inventory.DockerImages...)
	images = append(images, inventory.BoxLiteImages...)
	images = append(images, inventory.MicrosandboxImages...)
	images = append(images, inventory.VMImages...)
	for _, image := range images {
		for _, value := range []string{image.ID, image.Reference, image.Architecture, image.Size, image.Created, image.Format, image.Path, image.Source} {
			if utf8.RuneCountInString(value) > 1000 || strings.ContainsRune(value, '\x00') {
				return &ValidationError{Message: "服务器镜像信息无效"}
			}
		}
	}
	return nil
}

func NormalizeCredential(input *CredentialInput) {
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.ProviderID = strings.ToLower(strings.TrimSpace(input.ProviderID))
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	// Model selection belongs to a sandbox instance. Credentials only expose a
	// catalog of available models and intentionally have no platform default.
	input.ModelID = ""
	input.Secret = strings.TrimSpace(input.Secret)
}

func ValidateCredential(input CredentialInput, requireSecret bool) error {
	if n := utf8.RuneCountInString(input.ID); n < 2 || n > 64 || !idPattern.MatchString(input.ID) {
		return &ValidationError{Message: "凭据标识只能包含小写字母、数字和连字符，长度为 2 到 64"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "凭据名称需要 2 到 80 个字符"}
	}
	if input.ProviderID == "" {
		return &ValidationError{Message: "请选择 Agent Provider"}
	}
	switch input.Protocol {
	case "openai-responses", "openai-chat", "anthropic", "gemini":
	default:
		return &ValidationError{Message: "请选择有效的接口协议"}
	}
	if len(input.Endpoint) > 500 {
		return &ValidationError{Message: "接口地址过长"}
	}
	if input.Endpoint != "" {
		parsed, err := url.Parse(input.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return &ValidationError{Message: "接口地址必须是有效的 HTTP 或 HTTPS URL"}
		}
	}
	if requireSecret && input.Secret == "" {
		return &ValidationError{Message: "请填写 API Key"}
	}
	if len(input.Secret) > 16*1024 {
		return &ValidationError{Message: "API Key 过长"}
	}
	if strings.ContainsAny(input.Secret, "\r\n") {
		return &ValidationError{Message: "API Key 不能包含换行符"}
	}
	return nil
}

func NormalizeNetworkProxy(input *NetworkProxyInput) {
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.Scheme = strings.ToLower(strings.TrimSpace(input.Scheme))
	input.Host = strings.Trim(strings.TrimSpace(input.Host), "[]")
	input.Username = strings.TrimSpace(input.Username)
	seen := make(map[string]struct{}, len(input.NoProxy))
	noProxy := make([]string, 0, len(input.NoProxy))
	for _, entry := range input.NoProxy {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		noProxy = append(noProxy, entry)
	}
	input.NoProxy = noProxy
}

func ValidateNetworkProxy(input NetworkProxyInput) error {
	if n := utf8.RuneCountInString(input.ID); n < 2 || n > 64 || !idPattern.MatchString(input.ID) {
		return &ValidationError{Message: "代理标识只能包含小写字母、数字和连字符，长度为 2 到 64"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "代理名称需要 2 到 80 个字符"}
	}
	if input.Scheme != "http" && input.Scheme != "https" {
		return &ValidationError{Message: "第一版代理协议只支持 HTTP 或 HTTPS"}
	}
	if input.Port < 1 || input.Port > 65535 {
		return &ValidationError{Message: "代理端口需要在 1 到 65535 之间"}
	}
	if input.Host == "" || utf8.RuneCountInString(input.Host) > 253 || strings.ContainsAny(input.Host, "\x00\r\n\t /\\@") {
		return &ValidationError{Message: "代理主机名或 IP 无效"}
	}
	if net.ParseIP(input.Host) == nil {
		parsed, err := url.Parse(input.Scheme + "://" + net.JoinHostPort(input.Host, strconv.Itoa(input.Port)))
		if err != nil || parsed.Hostname() != input.Host {
			return &ValidationError{Message: "代理主机名或 IP 无效"}
		}
	}
	if utf8.RuneCountInString(input.Username) > 512 || strings.ContainsAny(input.Username, "\x00\r\n") {
		return &ValidationError{Message: "代理用户名无效"}
	}
	if len(input.Password) > 16*1024 || strings.ContainsAny(input.Password, "\x00\r\n") {
		return &ValidationError{Message: "代理密码无效"}
	}
	if input.Password != "" && input.Username == "" {
		return &ValidationError{Message: "填写代理密码时也需要填写用户名"}
	}
	if len(input.NoProxy) > 100 {
		return &ValidationError{Message: "直连地址不能超过 100 个"}
	}
	for _, entry := range input.NoProxy {
		if entry == "*" || utf8.RuneCountInString(entry) > 255 || strings.ContainsAny(entry, "\x00\r\n\t ,") {
			return &ValidationError{Message: "直连地址格式无效，请每行填写一个主机、IP 或 CIDR"}
		}
	}
	return nil
}

func NormalizeCredentialModel(input *CredentialModelInput) {
	input.ID = strings.TrimSpace(strings.TrimPrefix(input.ID, "models/"))
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = input.ID
	}
}

func ValidateCredentialModel(input CredentialModelInput) error {
	if n := utf8.RuneCountInString(input.ID); n < 1 || n > 256 || strings.ContainsAny(input.ID, "\r\n\t") {
		return &ValidationError{Message: "模型 ID 无效"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 1 || n > 160 {
		return &ValidationError{Message: "模型名称需要 1 到 160 个字符"}
	}
	return nil
}

func Validate(input Input) error {
	if !isKind(input.Kind) {
		return &ValidationError{Message: "资源类型无效"}
	}
	if n := utf8.RuneCountInString(input.ID); n < 2 || n > 64 || !idPattern.MatchString(input.ID) {
		return &ValidationError{Message: "标识只能包含小写字母、数字和连字符，长度为 2 到 64"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "名称需要 2 到 80 个字符"}
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return &ValidationError{Message: "简介不能超过 500 个字符"}
	}
	if input.Kind != KindProject && input.Kind != KindImage && input.ProjectID == nil {
		return &ValidationError{Message: "请选择所属项目"}
	}
	if input.Kind == KindImage {
		if err := require(input.Spec, "reference", "请填写镜像引用"); err != nil {
			return err
		}
		reference, _ := input.Spec["reference"].(string)
		if strings.ContainsAny(reference, " \t\r\n") {
			return &ValidationError{Message: "镜像引用不能包含空白字符"}
		}
		architecture, _ := input.Spec["architecture"].(string)
		if architecture != "all" && architecture != "amd64" && architecture != "arm64" {
			return &ValidationError{Message: "镜像架构无效"}
		}
		modes := stringList(input.Spec["modes"])
		if len(modes) == 0 {
			return &ValidationError{Message: "请至少选择一种兼容的隔离类型"}
		}
		for _, mode := range modes {
			if mode != "docker" && mode != "vm" {
				return &ValidationError{Message: "镜像兼容类型无效"}
			}
		}
	}
	if input.Kind == KindMCP {
		transport, _ := input.Spec["transport"].(string)
		if transport != "stdio" && transport != "http" {
			return &ValidationError{Message: "MCP transport 只能是 stdio 或 http"}
		}
		if transport == "stdio" {
			return require(input.Spec, "command", "stdio MCP 需要启动命令")
		}
		return require(input.Spec, "url", "HTTP MCP 需要 URL")
	}
	if input.Kind == KindRuntime {
		serverID, _ := input.Spec["serverId"].(string)
		if _, err := uuid.Parse(serverID); err != nil {
			return &ValidationError{Message: "请选择运行服务器"}
		}
		driver, _ := input.Spec["driver"].(string)
		switch driver {
		case "docker", "boxlite", "microsandbox", "vm":
		default:
			return &ValidationError{Message: "Runtime 驱动无效"}
		}
		if err := require(input.Spec, "imageReference", "请选择服务器上的镜像"); err != nil {
			return err
		}
		if err := validateAgentTools(input.Spec); err != nil {
			return err
		}
		if err := validateEnvironmentVariables(input.Spec); err != nil {
			return err
		}
		if err := validateNetworkSpec(input.Spec); err != nil {
			return err
		}
		if err := validateDesktopSpec(input.Spec); err != nil {
			return err
		}
	}
	if input.Kind == KindSandbox {
		if err := require(input.Spec, "runtimeId", "请选择沙箱模板"); err != nil {
			return err
		}
		serverID, _ := input.Spec["serverId"].(string)
		if _, err := uuid.Parse(serverID); err != nil {
			return &ValidationError{Message: "请选择目标服务器"}
		}
		if err := validateAgentTools(input.Spec); err != nil {
			return err
		}
		if err := validateEnvironmentVariables(input.Spec); err != nil {
			return err
		}
		if err := validateNetworkSpec(input.Spec); err != nil {
			return err
		}
		if err := validateDesktopSpec(input.Spec); err != nil {
			return err
		}
	}
	if input.Kind == KindVariable {
		if err := require(input.Spec, "key", "请填写环境变量名"); err != nil {
			return err
		}
		return require(input.Spec, "reference", "请填写变量或密钥引用")
	}
	return nil
}

func validateDesktopSpec(spec map[string]any) error {
	value, exists := spec["desktop"]
	if !exists || value == nil {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return &ValidationError{Message: "图形桌面配置无效"}
	}
	return nil
}

func validateNetworkSpec(spec map[string]any) error {
	network, _ := spec["network"].(string)
	if network != "" && network != "none" && network != "restricted" && network != "egress" {
		return &ValidationError{Message: "网络策略无效"}
	}
	proxyID, _ := spec["proxyId"].(string)
	proxyID = strings.TrimSpace(proxyID)
	if proxyID != "" && !idPattern.MatchString(proxyID) {
		return &ValidationError{Message: "网络代理引用无效"}
	}
	if proxyID != "" && network == "none" {
		return &ValidationError{Message: "完全隔离的环境不能同时使用网络代理"}
	}
	return nil
}

func validateAgentTools(spec map[string]any) error {
	for _, tool := range stringList(spec["agentTools"]) {
		if !allowedAgentTools[tool] {
			return &ValidationError{Message: "Agent 工具无效"}
		}
	}
	return nil
}

func validateEnvironmentVariables(spec map[string]any) error {
	raw, exists := spec["environmentVariables"]
	if !exists || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return &ValidationError{Message: "环境变量配置无效"}
	}
	if len(items) > 100 {
		return &ValidationError{Message: "环境变量不能超过 100 个"}
	}
	names := make(map[string]struct{}, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return &ValidationError{Message: "环境变量配置无效"}
		}
		name, nameOK := entry["name"].(string)
		value, valueOK := entry["value"].(string)
		if !nameOK || !valueOK || !environmentVariableNamePattern.MatchString(name) {
			return &ValidationError{Message: "环境变量名格式不正确"}
		}
		if strings.HasPrefix(name, "AGENTBOX_") {
			return &ValidationError{Message: "AGENTBOX_ 前缀由平台保留"}
		}
		if _, duplicate := names[name]; duplicate {
			return &ValidationError{Message: "环境变量名不能重复"}
		}
		if utf8.RuneCountInString(value) > 16*1024 || strings.ContainsAny(value, "\x00\r\n") {
			return &ValidationError{Message: "环境变量值无效"}
		}
		names[name] = struct{}{}
	}
	return nil
}

func require(spec map[string]any, key, message string) error {
	value, _ := spec[key].(string)
	if strings.TrimSpace(value) == "" {
		return &ValidationError{Message: message}
	}
	return nil
}

func stringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return strings
		}
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func isKind(kind Kind) bool {
	switch kind {
	case KindProject, KindImage, KindRuntime, KindSkill, KindMCP, KindSandbox, KindVariable:
		return true
	default:
		return false
	}
}
