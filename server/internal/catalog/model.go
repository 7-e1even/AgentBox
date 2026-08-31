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

type Catalog struct {
	Providers []Provider `json:"providers"`
}
