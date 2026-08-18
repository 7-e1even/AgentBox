export type AgentToolOption = {
  value: AgentToolId
  label: string
}

export type AgentProtocol =
  | "openai-responses"
  | "openai-chat"
  | "anthropic"
  | "gemini"

export const supportedAgentToolIds = [
  "claude-code",
  "codex",
  "deepseek-harness",
  "gemini-cli",
  "kimi",
  "opencode",
  "pi",
  "reasonix",
] as const

export type AgentToolId = (typeof supportedAgentToolIds)[number]

export const agentToolOptions: AgentToolOption[] = [
  { value: "claude-code", label: "Claude Code" },
  { value: "codex", label: "Codex" },
  { value: "deepseek-harness", label: "DeepSeek Harness" },
  { value: "gemini-cli", label: "Gemini CLI" },
  { value: "kimi", label: "Kimi Code" },
  { value: "opencode", label: "OpenCode" },
  { value: "pi", label: "Pi" },
  { value: "reasonix", label: "Reasonix" },
]

const supportedAgentTools = new Set<string>(supportedAgentToolIds)

const agentToolProtocols: Record<AgentToolId, readonly AgentProtocol[]> = {
  "claude-code": ["anthropic", "openai-responses", "openai-chat"],
  codex: ["openai-responses", "anthropic", "openai-chat"],
  "deepseek-harness": [
    "anthropic",
    "openai-chat",
    "openai-responses",
    "gemini",
  ],
  "gemini-cli": ["gemini", "anthropic", "openai-chat", "openai-responses"],
  kimi: ["anthropic", "openai-chat", "openai-responses"],
  opencode: ["anthropic", "openai-chat", "openai-responses", "gemini"],
  pi: ["anthropic", "openai-chat", "openai-responses", "gemini"],
  reasonix: ["anthropic", "openai-chat", "openai-responses"],
}

export function supportedAgentToolList(value: unknown) {
  return Array.isArray(value)
    ? value.filter(
        (item): item is AgentToolId =>
          typeof item === "string" && supportedAgentTools.has(item)
      )
    : []
}

export function incompatibleAgentTools(
  tools: unknown,
  protocols: readonly AgentProtocol[]
) {
  const available = new Set(protocols)
  return supportedAgentToolList(tools).filter(
    (tool) => !agentToolProtocols[tool].some((protocol) => available.has(protocol))
  )
}
