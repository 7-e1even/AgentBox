package catalog

var BuiltinCatalog = Catalog{
	Providers: []Provider{
		{ID: "openai", Name: "OpenAI", Mark: "OA", Description: "通用推理、工具调用与结构化输出", Models: []ProviderModel{
			{ID: "gpt-5", Name: "GPT-5", Context: "长上下文", Note: "推荐"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Context: "长上下文", Note: "快速"},
		}},
		{ID: "anthropic", Name: "Anthropic", Mark: "AN", Description: "长文本理解、分析与代码任务", Models: []ProviderModel{
			{ID: "claude-sonnet", Name: "Claude Sonnet", Context: "长上下文", Note: "均衡"},
			{ID: "claude-haiku", Name: "Claude Haiku", Context: "长上下文", Note: "快速"},
		}},
		{ID: "google", Name: "Google", Mark: "GO", Description: "多模态理解与大上下文任务", Models: []ProviderModel{
			{ID: "gemini-pro", Name: "Gemini Pro", Context: "超长上下文", Note: "推理"},
			{ID: "gemini-flash", Name: "Gemini Flash", Context: "长上下文", Note: "快速"},
		}},
		{ID: "deepseek", Name: "DeepSeek", Mark: "DS", Description: "推理、编程与成本敏感任务", Models: []ProviderModel{
			{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", Context: "长上下文", Note: "推理"},
			{ID: "deepseek-chat", Name: "DeepSeek Chat", Context: "长上下文", Note: "通用"},
		}},
	},
}
