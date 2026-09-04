import { describe, expect, it } from "vitest"

import { incompatibleAgentTools } from "./agent-tools"

describe("incompatibleAgentTools", () => {
  it("allows agents to authenticate themselves when no model service is configured", () => {
    expect(incompatibleAgentTools(["codex"], [])).toEqual([])
  })

  it("uses the LLM facade so one Anthropic credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        [
          "claude-code",
          "codex",
          "deepseek-harness",
          "gemini-cli",
          "grok",
          "kimi",
          "opencode",
          "pi",
          "reasonix",
        ],
        ["anthropic"]
      )
    ).toEqual([])
  })

  it("uses the LLM facade so one Responses credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        [
          "claude-code",
          "codex",
          "deepseek-harness",
          "gemini-cli",
          "grok",
          "kimi",
          "opencode",
          "pi",
          "reasonix",
        ],
        ["openai-responses"]
      )
    ).toEqual([])
  })

  it("uses the LLM facade so one Chat credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        [
          "claude-code",
          "codex",
          "deepseek-harness",
          "gemini-cli",
          "grok",
          "kimi",
          "opencode",
          "pi",
          "reasonix",
        ],
        ["openai-chat"]
      )
    ).toEqual([])
  })

  it("routes Gemini credentials through the Chat facade for DeepSeek Harness", () => {
    expect(incompatibleAgentTools(["deepseek-harness"], ["gemini"])).toEqual([])
  })

  it("routes Grok Build through the Responses facade", () => {
    expect(incompatibleAgentTools(["grok"], ["openai-responses"])).toEqual([])
    expect(incompatibleAgentTools(["grok"], ["anthropic"])).toEqual([])
  })
})
