package agent

import "time"

type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

type Input struct {
	ProjectID     string   `json:"projectId"`
	RuntimeID     string   `json:"runtimeId"`
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	Description   string   `json:"description"`
	Avatar        string   `json:"avatar"`
	ProviderID    string   `json:"providerId"`
	ModelID       string   `json:"modelId"`
	CredentialID  *string  `json:"credentialId"`
	SystemPrompt  string   `json:"systemPrompt"`
	SkillIDs      []string `json:"skillIds"`
	MCPServerIDs  []string `json:"mcpServerIds"`
	VariableIDs   []string `json:"variableIds"`
	CustomArgs    []string `json:"customArgs"`
	Temperature   float64  `json:"temperature"`
	MaxSteps      int      `json:"maxSteps"`
	Concurrency   int      `json:"concurrency"`
	SandboxPolicy string   `json:"sandboxPolicy"`
	Status        Status   `json:"status"`
}

type UpdateInput struct {
	Input
	Version int `json:"version"`
}

type Agent struct {
	Input
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProviderModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Context string `json:"context"`
	Note    string `json:"note"`
}

type Provider struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Mark        string          `json:"mark"`
	Description string          `json:"description"`
	Models      []ProviderModel `json:"models"`
}

type Credential struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProviderID  string `json:"providerId"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
}

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Category    string `json:"category"`
}

type MCPServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Transport   string `json:"transport"`
	ToolCount   int    `json:"toolCount"`
	Status      string `json:"status"`
}

type Catalog struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Providers   []Provider   `json:"providers"`
	Credentials []Credential `json:"credentials"`
	Skills      []Skill      `json:"skills"`
	MCPServers  []MCPServer  `json:"mcpServers"`
}
