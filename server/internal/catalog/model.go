package catalog

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
	Providers  []Provider  `json:"providers"`
	Skills     []Skill     `json:"skills"`
	MCPServers []MCPServer `json:"mcpServers"`
}
