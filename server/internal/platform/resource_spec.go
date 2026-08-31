package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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
	Version      string `json:"version,omitempty"`
	Category     string `json:"category,omitempty"`
	Source       string `json:"source,omitempty"`
	Path         string `json:"path,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type MCPSpec struct {
	Transport string `json:"transport"`
	Command   string `json:"command,omitempty"`
	Args      string `json:"args,omitempty"`
	URL       string `json:"url,omitempty"`
	Headers   string `json:"headers,omitempty"`
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
