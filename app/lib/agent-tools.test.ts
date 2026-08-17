import { describe, expect, it } from "vitest"

import { incompatibleAgentTools } from "./agent-tools"

describe("incompatibleAgentTools", () => {
  it("uses the LLM facade so one Anthropic credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        ["claude-code", "codex", "gemini-cli", "kimi", "opencode", "pi", "reasonix"],
        ["anthropic"]
      )
    ).toEqual([])
  })

  it("uses the LLM facade so one Responses credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        ["claude-code", "codex", "gemini-cli", "kimi", "opencode", "pi", "reasonix"],
        ["openai-responses"]
      )
    ).toEqual([])
  })

  it("uses the LLM facade so one Chat credential can serve every curated Agent", () => {
    expect(
      incompatibleAgentTools(
        ["claude-code", "codex", "gemini-cli", "kimi", "opencode", "pi", "reasonix"],
        ["openai-chat"]
      )
    ).toEqual([])
  })
})
