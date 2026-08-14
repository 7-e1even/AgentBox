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
	Skills: []Skill{
		{ID: "web-research", Name: "Web Research", Description: "规划检索、比较来源并输出带证据的研究结论。", Version: "1.4.0", Category: "研究"},
		{ID: "code-review", Name: "Code Review", Description: "审查变更、定位风险并生成可执行的代码反馈。", Version: "2.1.0", Category: "开发"},
		{ID: "document-writer", Name: "Document Writer", Description: "按模板生成结构清晰、语气一致的业务文档。", Version: "1.8.2", Category: "内容"},
		{ID: "data-analysis", Name: "Data Analysis", Description: "清洗数据、执行分析并解释关键指标。", Version: "1.2.1", Category: "数据"},
		{ID: "task-planner", Name: "Task Planner", Description: "拆解复杂目标，维护检查点并跟踪交付状态。", Version: "1.3.0", Category: "通用"},
		{ID: "support-tone", Name: "Support Tone", Description: "使用准确、克制且友好的客户支持表达。", Version: "1.1.0", Category: "服务"},
	},
	MCPServers: []MCPServer{
		{ID: "filesystem", Name: "Workspace Files", Description: "读取与编辑授权工作区中的文件。", Transport: "stdio", ToolCount: 8, Status: "ready"},
		{ID: "github", Name: "GitHub", Description: "访问仓库、Issue、Pull Request 与 Actions。", Transport: "http", ToolCount: 14, Status: "ready"},
		{ID: "browser", Name: "Browser", Description: "打开网页并与浏览器页面交互。", Transport: "http", ToolCount: 9, Status: "ready"},
		{ID: "postgres", Name: "Postgres", Description: "在受控连接上查询业务数据库。", Transport: "stdio", ToolCount: 5, Status: "attention"},
	},
}
