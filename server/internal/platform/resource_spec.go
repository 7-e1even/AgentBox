package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
)

const (
	MaxMCPBindings      = 32
	MaxVariableBindings = 100
)

// Spec stays JSON at the persistence boundary. Every mutation decodes it into
// one of these kind-specific contracts before validating references or storing it.
type ProjectSpec struct {
	Emoji string `json:"emoji,omitempty"`
}

type ImageSpec struct {
	Reference    string   `json:"reference"`
	Architecture string   `json:"architecture"`
	Modes        []string `json:"modes"`
}

type EnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ExecutionSpec struct {
	ServerID             string                `json:"serverId"`
	Driver               string                `json:"driver,omitempty"`
	ImageReference       string                `json:"imageReference,omitempty"`
	ImageID              string                `json:"imageId,omitempty"`
	Workdir              string                `json:"workdir,omitempty"`
	Setup                string                `json:"setup,omitempty"`
	CPU                  string                `json:"cpu,omitempty"`
	Memory               string                `json:"memory,omitempty"`
	Desktop              *bool                 `json:"desktop,omitempty"`
	Network              string                `json:"network,omitempty"`
	ProxyID              string                `json:"proxyId,omitempty"`
	AgentTools           []string              `json:"agentTools,omitempty"`
	SkillIDs             []string              `json:"skillIds,omitempty"`
	MCPServerIDs         []string              `json:"mcpServerIds,omitempty"`
	VariableIDs          []string              `json:"variableIds,omitempty"`
	ExtensionIDs         []string              `json:"extensionIds,omitempty"`
	CredentialIDs        []string              `json:"credentialIds,omitempty"`
	EnvironmentVariables []EnvironmentVariable `json:"environmentVariables,omitempty"`
	ModelBindings        map[string]string     `json:"modelBindings,omitempty"`
	Workspace            string                `json:"workspace,omitempty"`
}

type RuntimeSpec struct{ ExecutionSpec }

type SandboxSpec struct {
	ExecutionSpec
	RuntimeID string `json:"runtimeId"`
	Policy    string `json:"policy,omitempty"`
}

type SkillSpec struct {
	Version       string      `json:"version,omitempty"`
	Category      string      `json:"category,omitempty"`
	Source        string      `json:"source,omitempty"`
	Path          string      `json:"path,omitempty"`
	License       string      `json:"license,omitempty"`
	Compatibility string      `json:"compatibility,omitempty"`
	BundleDigest  string      `json:"bundleDigest,omitempty"`
	FileCount     int         `json:"fileCount,omitempty"`
	DecodedBytes  int         `json:"decodedBytes,omitempty"`
	Instructions  string      `json:"instructions,omitempty"`
	Files         []SkillFile `json:"files,omitempty"`
}

type SkillFile struct {
	Path       string `json:"path"`
	Content    string `json:"content"` // Base64 preserves scripts and binary assets.
	Executable bool   `json:"executable,omitzero"`
}

type MCPSpec struct {
	Transport string      `json:"transport"`
	Command   string      `json:"command,omitempty"`
	Args      []string    `json:"args,omitempty"`
	URL       string      `json:"url,omitempty"`
	Headers   []MCPHeader `json:"headers,omitempty"`
	Cwd       string      `json:"cwd,omitempty"`
}

type MCPHeader struct {
	Name      string `json:"name"`
	ValueFrom string `json:"valueFrom"`
}

type VariableSpec struct {
	Key       string `json:"key"`
	Mode      string `json:"mode,omitempty"`
	Reference string `json:"reference"`
}

type ResourceFilter struct {
	Kind      Kind
	ProjectID string
}

func (f ResourceFilter) Validate() error {
	if f.Kind != "" && !isKind(f.Kind) {
		return &ValidationError{Message: "资源类型无效"}
	}
	if f.ProjectID != "" && (len(f.ProjectID) > 64 || !idPattern.MatchString(f.ProjectID)) {
		return &ValidationError{Message: "项目标识无效"}
	}
	return nil
}

// These fields are observations or provenance, never desired configuration.
var sandboxObservedSpecFields = []string{
	"status", "message", "externalId", "provisioning", "appliedProxyId", "proxyOperation",
	"agentToolVersions", "agentToolOperation", "automationId", "automationRunId",
	"extensionSnapshots", "extensionStates", "runtimeModelSources", "runtimeModelSourcesComplete",
	"runtimeModelTokenEpoch", "credentialedProxyIdAtCreation",
	"capabilitiesPendingRestart", "capabilityDigest", "capabilitiesAppliedAt", "capabilityRevision",
}

func DesiredResourceSpec(kind Kind, spec map[string]any) map[string]any {
	result := maps.Clone(spec)
	if kind == KindSandbox {
		for _, key := range sandboxObservedSpecFields {
			delete(result, key)
		}
	}
	return result
}

// DecodeResourceSpec accepts known read-only fields from older clients but does
// not include them in the decoded desired configuration. Unknown fields and
// malformed types are rejected instead of disappearing during normalization.
func DecodeResourceSpec(input Input) (any, error) {
	var target any
	switch input.Kind {
	case KindProject:
		target = &ProjectSpec{}
	case KindImage:
		target = &ImageSpec{}
	case KindRuntime:
		target = &RuntimeSpec{}
	case KindSandbox:
		target = &SandboxSpec{}
	case KindSkill:
		target = &SkillSpec{}
	case KindMCP:
		target = &MCPSpec{}
	case KindVariable:
		target = &VariableSpec{}
	case KindExtension:
		target = &ExtensionSpec{}
	default:
		return nil, &ValidationError{Message: "资源类型无效"}
	}
	encoded, err := json.Marshal(DesiredResourceSpec(input.Kind, input.Spec))
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(target)
	}
	if err != nil {
		return nil, &ValidationError{Message: fmt.Sprintf("%s spec 格式无效: %v", input.Kind, err)}
	}
	return target, nil
}

// CanonicalizeResourceSpec converts compatibility input shapes to the single
// representation persisted and returned by current APIs.
func CanonicalizeResourceSpec(input *Input) error {
	if input.Kind != KindMCP && input.Kind != KindSkill && input.Kind != KindVariable {
		return nil
	}
	decoded, err := DecodeResourceSpec(*input)
	if err != nil {
		return err
	}
	switch spec := decoded.(type) {
	case *MCPSpec:
		spec.Transport = strings.ToLower(strings.TrimSpace(spec.Transport))
		spec.Command = strings.TrimSpace(spec.Command)
		spec.URL = strings.TrimSpace(spec.URL)
		spec.Cwd = strings.TrimSpace(spec.Cwd)
		for index := range spec.Headers {
			spec.Headers[index].Name = http.CanonicalHeaderKey(strings.TrimSpace(spec.Headers[index].Name))
			spec.Headers[index].ValueFrom = strings.TrimSpace(spec.Headers[index].ValueFrom)
		}
	case *SkillSpec:
		if err := CanonicalizeSkillSpec(input.ID, spec); err != nil {
			return err
		}
		description, err := SkillCatalogDescription(*spec)
		if err != nil {
			return err
		}
		input.Description = description
	case *VariableSpec:
		spec.Key = strings.TrimSpace(spec.Key)
		spec.Mode = strings.ToLower(strings.TrimSpace(spec.Mode))
		spec.Reference = strings.TrimSpace(spec.Reference)
		if spec.Mode == "" {
			scheme, _, ok := ParseMCPValueReference(spec.Reference)
			if ok && scheme == "env" {
				spec.Mode = "value-ref"
			} else if ok && scheme == "secret" {
				spec.Mode = "secret-ref"
			}
		}
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return fmt.Errorf("encode canonical %s spec: %w", input.Kind, err)
	}
	var spec map[string]any
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return fmt.Errorf("decode canonical %s spec: %w", input.Kind, err)
	}
	input.Spec = spec
	return nil
}
