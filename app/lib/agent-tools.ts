export type AgentToolOption = {
  value: string
  label: string
  disabled?: boolean
}

export const commonAgentToolIds = [
  "codex",
  "claude-code",
  "gemini-cli",
  "opencode",
  "pi",
  "copilot-cli",
  "qwen-code",
]

export const multicaLinuxAgentToolIds = [
  "antigravity",
  "claude-code",
  "codebuddy",
  "codex",
  "copilot-cli",
  "cursor",
  "grok",
  "kimi",
  "omp",
  "openclaw",
  "opencode",
  "pi",
  "qoder-cli",
  "qoder-cn",
  "qwen-code",
  "qwenpaw",
  "reasonix",
]

export const agentToolOptions: AgentToolOption[] = [
  { value: "antigravity", label: "Antigravity" },
  { value: "claude-code", label: "Claude Code" },
  { value: "codebuddy", label: "CodeBuddy" },
  { value: "codex", label: "Codex" },
  { value: "copilot-cli", label: "GitHub Copilot CLI" },
  { value: "cursor", label: "Cursor CLI" },
  { value: "deveco", label: "DevEco Code（Linux 不支持）", disabled: true },
  { value: "gemini-cli", label: "Gemini CLI" },
  { value: "grok", label: "Grok CLI" },
  { value: "kimi", label: "Kimi Code CLI" },
  { value: "omp", label: "Oh-My-Pi" },
  { value: "openclaw", label: "OpenClaw" },
  { value: "opencode", label: "OpenCode" },
  { value: "pi", label: "Pi" },
  { value: "qoder-cli", label: "Qoder CLI" },
  { value: "qoder-cn", label: "Qoder CLI CN" },
  { value: "qwen-code", label: "Qwen Code" },
  { value: "qwenpaw", label: "QwenPaw" },
  { value: "reasonix", label: "Reasonix" },
  { value: "trae-cli", label: "TRAE CLI（需自带）", disabled: true },
]
