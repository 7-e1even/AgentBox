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
	KindProject   Kind = "project"
	KindImage     Kind = "image"
	KindRuntime   Kind = "runtime"
	KindSkill     Kind = "skill"
	KindMCP       Kind = "mcp"
	KindSandbox   Kind = "sandbox"
	KindVariable  Kind = "variable"
	KindExtension Kind = "extension"
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
	SpecVersion int            `json:"specVersion"`
	Spec        map[string]any `json:"spec"`
}

type Resource struct {
	Input
	Generation         int64     `json:"generation"`
	ObservedGeneration int64     `json:"observedGeneration"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
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

type NetworkProxyCheck struct {
	ID         string     `json:"checkId"`
	ProxyID    string     `json:"proxyId"`
	ServerID   string     `json:"serverId"`
	ServerName string     `json:"serverName"`
	Scope      string     `json:"scope"`
	Status     string     `json:"status"`
	OK         *bool      `json:"ok,omitempty"`
	LatencyMS  int64      `json:"latencyMs,omitzero"`
	Target     string     `json:"target"`
	StatusCode int        `json:"statusCode,omitzero"`
	Error      string     `json:"error,omitempty"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
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

// SandboxModelSourceInput redirects one credential slot already configured in
// a running sandbox to a different managed credential and model. The slot stays
// stable so existing Agent processes can keep using their current facade URL
// and runtime token.
type SandboxModelSourceInput struct {
	SlotCredentialID     string `json:"slotCredentialId"`
	CredentialID         string `json:"credentialId"`
	ModelID              string `json:"modelId"`
	ExpectedCredentialID string `json:"expectedCredentialId"`
	ExpectedModelID      string `json:"expectedModelId"`
}

type RuntimeModelSource struct {
	CredentialID string    `json:"credentialId"`
	ModelID      string    `json:"modelId"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type WorkerJob struct {
	ID                 string         `json:"id"`
	ResourceID         string         `json:"resourceId"`
	ResourceGeneration int64          `json:"resourceGeneration"`
	Action             string         `json:"action"`
	Payload            map[string]any `json:"payload"`
	LeaseGeneration    int            `json:"leaseGeneration"`
	LeaseExpiresAt     time.Time      `json:"leaseExpiresAt"`
	MaxAttempts        int            `json:"maxAttempts"`
}

type WorkerJobResult struct {
	// LeaseGeneration fences stale executors. Zero is accepted only for a
	// first-generation lease as a controlled bridge for older Workers.
	LeaseGeneration int                     `json:"leaseGeneration"`
	Success         bool                    `json:"success"`
	ExternalID      string                  `json:"externalId"`
	Message         string                  `json:"message"`
	ExitCode        *int                    `json:"exitCode,omitempty"`
	Output          string                  `json:"output,omitempty"`
	OutputTruncated bool                    `json:"outputTruncated,omitzero"`
	TimedOut        bool                    `json:"timedOut,omitzero"`
	Error           *WorkerJobError         `json:"error,omitempty"`
	AgentTools      []SandboxAgentToolState `json:"agentTools,omitempty"`
}

type WorkerJobControlInput struct {
	LeaseGeneration int `json:"leaseGeneration"`
}

type WorkerJobControl struct {
	CancelRequested bool `json:"cancelRequested"`
}

type SandboxAgentToolState struct {
	Tool            string    `json:"tool"`
	CurrentVersion  string    `json:"currentVersion,omitempty"`
	LatestVersion   string    `json:"latestVersion,omitempty"`
	PreviousVersion string    `json:"previousVersion,omitempty"`
	Status          string    `json:"status"`
	Message         string    `json:"message,omitempty"`
	Source          string    `json:"source,omitempty"`
	CheckedAt       time.Time `json:"checkedAt,omitzero"`
}

type WorkerJobError struct {
	Code      string            `json:"code"`
	Stage     string            `json:"stage,omitempty"`
	Retryable bool              `json:"retryable"`
	Details   map[string]string `json:"details,omitempty"`
}

type WorkerJobProgressInput struct {
	// LeaseGeneration follows the same first-generation compatibility rule as
	// WorkerJobResult. A reclaimed lease always requires its exact generation.
	LeaseGeneration int    `json:"leaseGeneration"`
	Stage           string `json:"stage"`
	Message         string `json:"message"`
	CacheStatus     string `json:"cacheStatus,omitempty"`
	CacheReason     string `json:"cacheReason,omitempty"`
	AgentTool       string `json:"agentTool,omitempty"`
	AgentToolStatus string `json:"agentToolStatus,omitempty"`
	ExtensionID     string `json:"extensionId,omitempty"`
	ExtensionStatus string `json:"extensionStatus,omitempty"`
	ExtensionOutput string `json:"extensionOutput,omitempty"`
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
	DurationMS int64      `json:"durationMs,omitzero"`
}

type ProvisioningProgress struct {
	CancellationSupported bool                      `json:"cancellationSupported,omitzero"`
	CancelRequested       bool                      `json:"cancelRequested,omitzero"`
	Stage                 string                    `json:"stage,omitempty"`
	Message               string                    `json:"message,omitempty"`
	Status                string                    `json:"status,omitempty"`
	CacheStatus           string                    `json:"cacheStatus,omitempty"`
	CacheReason           string                    `json:"cacheReason,omitempty"`
	StartedAt             *time.Time                `json:"startedAt,omitempty"`
	StageStartedAt        *time.Time                `json:"stageStartedAt,omitempty"`
	UpdatedAt             *time.Time                `json:"updatedAt,omitempty"`
	FinishedAt            *time.Time                `json:"finishedAt,omitempty"`
	DurationMS            int64                     `json:"durationMs,omitzero"`
	Timings               []ProvisioningStageTiming `json:"timings,omitempty"`
	AgentTools            []ProvisioningAgentTool   `json:"agentTools,omitempty"`
	Extensions            []ProvisioningExtension   `json:"extensions,omitempty"`
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func Normalize(input *Input) {
	if input.SpecVersion == 0 {
		input.SpecVersion = 1
	}
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
	if input.Kind == KindVariable {
		for _, key := range []string{"key", "reference"} {
			if value, ok := input.Spec[key].(string); ok {
				input.Spec[key] = strings.TrimSpace(value)
			}
		}
		if value, ok := input.Spec["mode"].(string); ok {
			input.Spec["mode"] = strings.ToLower(strings.TrimSpace(value))
		}
		mode, _ := input.Spec["mode"].(string)
		if strings.TrimSpace(mode) == "" {
			if reference, ok := input.Spec["reference"].(string); ok {
				if scheme, _, valid := ParseMCPValueReference(reference); valid {
					if scheme == "env" {
						input.Spec["mode"] = "value-ref"
					} else {
						input.Spec["mode"] = "secret-ref"
					}
				}
			}
		}
	}
	normalizeExtensionSpec(input)
	if input.Kind == KindRuntime || input.Kind == KindSandbox {
		for _, key := range []string{"agentTools", "skillIds", "mcpServerIds", "variableIds"} {
			normalizeResourceIDList(input.Spec, key)
		}
		// Do not normalize malformed values into a valid empty list before validation.
		if value := input.Spec["environmentVariables"]; value == nil {
			input.Spec["environmentVariables"] = SandboxEnvironmentVariables(nil)
		} else if _, ok := value.([]any); ok {
			input.Spec["environmentVariables"] = SandboxEnvironmentVariables(value)
		}
	}
}

func normalizeResourceIDList(spec map[string]any, key string) {
	raw, exists := spec[key]
	if !exists {
		return
	}
	items, ok := raw.([]any)
	if !ok {
		if strings, stringsOK := raw.([]string); stringsOK {
			items = make([]any, len(strings))
			for index, value := range strings {
				items[index] = value
			}
		} else {
			return
		}
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]any, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	spec[key] = result
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
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
			return &ValidationError{Message: "接口地址必须是有效的 HTTPS URL"}
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
	if input.Scheme != "http" && input.Scheme != "https" &&
		input.Scheme != "socks5" && input.Scheme != "socks5h" {
		return &ValidationError{Message: "代理协议只支持 HTTP、HTTPS、SOCKS5 或 SOCKS5H"}
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

func NormalizeSandboxModelSourceInput(input *SandboxModelSourceInput) {
	input.SlotCredentialID = strings.ToLower(strings.TrimSpace(input.SlotCredentialID))
	input.CredentialID = strings.ToLower(strings.TrimSpace(input.CredentialID))
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.ExpectedCredentialID = strings.ToLower(strings.TrimSpace(input.ExpectedCredentialID))
	input.ExpectedModelID = strings.TrimSpace(input.ExpectedModelID)
}

func ValidateSandboxModelSourceInput(input SandboxModelSourceInput) error {
	for _, credentialID := range []string{
		input.SlotCredentialID,
		input.CredentialID,
		input.ExpectedCredentialID,
	} {
		if n := utf8.RuneCountInString(credentialID); n < 2 || n > 64 || !idPattern.MatchString(credentialID) {
			return &ValidationError{Message: "模型服务标识无效"}
		}
	}
	for _, modelID := range []string{input.ModelID, input.ExpectedModelID} {
		if n := utf8.RuneCountInString(modelID); n < 1 || n > 256 || strings.ContainsAny(modelID, "\r\n\t") {
			return &ValidationError{Message: "模型 ID 无效"}
		}
	}
	return nil
}

func Validate(input Input) error {
	if input.SpecVersion != 0 && input.SpecVersion != 1 {
		return &ValidationError{Message: "不支持的资源 specVersion"}
	}
	decodedSpec, err := DecodeResourceSpec(input)
	if err != nil {
		return err
	}
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
	if input.Kind == KindSkill {
		return ValidateSkillResource(input.ID, input.Description, *decodedSpec.(*SkillSpec))
	}
	if input.Kind == KindExtension {
		return ValidateExtensionSpec(*decodedSpec.(*ExtensionSpec), input.Enabled)
	}
	if input.Kind == KindRuntime || input.Kind == KindSandbox {
		if err := validateExtensionIDs(input.Spec); err != nil {
			return err
		}
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
		return ValidateMCPSpec(*decodedSpec.(*MCPSpec))
	}
	if input.Kind == KindRuntime {
		spec := decodedSpec.(*RuntimeSpec).ExecutionSpec
		if err := validateCapabilityBindings(spec); err != nil {
			return err
		}
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
		network, _ := input.Spec["network"].(string)
		if err := ValidateNetworkPolicyForDriver(driver, network); err != nil {
			return err
		}
		if err := validateDesktopSpec(input.Spec); err != nil {
			return err
		}
	}
	if input.Kind == KindSandbox {
		spec := decodedSpec.(*SandboxSpec).ExecutionSpec
		if err := validateCapabilityBindings(spec); err != nil {
			return err
		}
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
		return ValidateVariableSpec(*decodedSpec.(*VariableSpec))
	}
	return nil
}

func validateCapabilityBindings(spec ExecutionSpec) error {
	if len(spec.SkillIDs) > MaxSkillBindings {
		return &ValidationError{Message: "一个环境最多绑定 32 个 Skill"}
	}
	if len(spec.MCPServerIDs) > MaxMCPBindings {
		return &ValidationError{Message: "一个环境最多绑定 32 个 MCP Server"}
	}
	if len(spec.VariableIDs) > MaxVariableBindings {
		return &ValidationError{Message: "一个环境最多绑定 100 个 Variable"}
	}
	for label, ids := range map[string][]string{
		"Skill": spec.SkillIDs, "MCP": spec.MCPServerIDs, "Variable": spec.VariableIDs,
	} {
		seen := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if !idPattern.MatchString(id) || len(id) > 64 {
				return &ValidationError{Message: label + " 引用标识无效"}
			}
			if _, duplicate := seen[id]; duplicate {
				return &ValidationError{Message: label + " 引用不能重复"}
			}
			seen[id] = struct{}{}
		}
	}
	if len(spec.MCPServerIDs) == 0 {
		return nil
	}
	for _, tool := range spec.AgentTools {
		if SupportsMCPAgentTool(tool) {
			return nil
		}
	}
	return &ValidationError{Message: "已选择 MCP Server，但当前 Agent 均不支持 MCP；请选择 Claude Code、Codex、DeepSeek Harness、Gemini CLI 或 OpenCode"}
}

func SupportsMCPAgentTool(tool string) bool {
	switch tool {
	case "claude-code", "codex", "deepseek-harness", "gemini-cli", "opencode":
		return true
	default:
		return false
	}
}

func ValidateVariableSpec(spec VariableSpec) error {
	if len(spec.Key) > MaxVariableNameLength || !environmentVariableNamePattern.MatchString(spec.Key) {
		return &ValidationError{Message: "Variable key 必须是有效的环境变量名"}
	}
	if spec.Key == "IS_SANDBOX" || strings.HasPrefix(spec.Key, "AGENTBOX_") {
		return &ValidationError{Message: "该 Variable key 由平台保留"}
	}
	scheme, source, ok := ParseMCPValueReference(spec.Reference)
	if !ok || len(source) > MaxVariableNameLength {
		return &ValidationError{Message: "Variable reference 只能是 env://NAME 或 secret://NAME"}
	}
	mode := spec.Mode
	if mode == "" {
		if scheme == "env" {
			mode = "value-ref"
		} else {
			mode = "secret-ref"
		}
	}
	switch mode {
	case "value-ref":
		if scheme != "env" {
			return &ValidationError{Message: "value-ref Variable 必须使用 env://NAME"}
		}
	case "secret-ref":
		if scheme != "secret" {
			return &ValidationError{Message: "secret-ref Variable 必须使用 secret://NAME"}
		}
	default:
		return &ValidationError{Message: "Variable mode 只能是 value-ref 或 secret-ref"}
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

func ValidateNetworkPolicyForDriver(driver, network string) error {
	if network == "restricted" && driver != "boxlite" {
		return &ValidationError{Message: "受限网络仅支持 BoxLite 驱动"}
	}
	return nil
}

func EffectiveNetworkPolicy(driver, network string) string {
	if network != "" {
		return network
	}
	if driver == "boxlite" {
		return "restricted"
	}
	return "none"
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
	case KindProject, KindImage, KindRuntime, KindSkill, KindMCP, KindSandbox, KindVariable, KindExtension:
		return true
	default:
		return false
	}
}
