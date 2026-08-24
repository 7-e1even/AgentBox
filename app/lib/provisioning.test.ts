import { describe, expect, it } from "vitest"

import { provisioningProgressSchema } from "./platform-schema"
import {
  provisioningAgentToolStatusLabel,
  provisioningDuration,
} from "./provisioning"

describe("provisioning progress", () => {
  it("parses per-Agent installation progress", () => {
    const progress = provisioningProgressSchema.parse({
      stage: "agent-image",
      status: "running",
      agentTools: [
        {
          tool: "codex",
          status: "verifying",
          message: "正在验证 Codex",
          startedAt: "2026-08-24T08:00:00Z",
          durationMs: 12_000,
        },
      ],
    })

    expect(progress.agentTools[0]).toMatchObject({
      tool: "codex",
      status: "verifying",
      finishedAt: null,
      durationMs: 12_000,
    })
  })

  it("keeps old provisioning payloads backward compatible", () => {
    expect(provisioningProgressSchema.parse({}).agentTools).toEqual([])
  })

  it("formats Agent status and elapsed time", () => {
    expect(provisioningAgentToolStatusLabel("cached")).toBe("缓存复用")
    expect(provisioningAgentToolStatusLabel("running")).toBe("安装中")
    expect(provisioningDuration(65_000)).toBe("1 分 5 秒")
  })
})
