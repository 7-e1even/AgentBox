import { describe, expect, it } from "vitest"

import { provisioningProgressSchema } from "./platform-schema"
import {
  provisioningAgentToolStatusLabel,
  provisioningDuration,
  sandboxInstallCancellation,
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
    expect(provisioningProgressSchema.parse({}).cancellationSupported).toBe(
      false
    )
    expect(provisioningProgressSchema.parse({}).cancelRequested).toBe(false)
  })

  it("preserves cancellation capability and request state from the backend", () => {
    expect(
      provisioningProgressSchema.parse({
        status: "cancelling",
        cancellationSupported: true,
        cancelRequested: true,
      })
    ).toMatchObject({
      status: "cancelling",
      cancellationSupported: true,
      cancelRequested: true,
    })
  })

  it("allows queued creation cancellation before a Worker claims the job", () => {
    expect(sandboxInstallCancellation({ status: "requested" })).toBe(
      "available"
    )
  })

  it("requires Worker capability before cancelling a running installation", () => {
    expect(sandboxInstallCancellation({ status: "starting" })).toBe(
      "unsupported"
    )
    expect(
      sandboxInstallCancellation({
        status: "starting",
        provisioning: { status: "running", cancellationSupported: true },
      })
    ).toBe("available")
  })

  it("shows cancellation in progress without offering another request", () => {
    expect(sandboxInstallCancellation({ status: "cancelling" })).toBe(
      "cancelling"
    )
  })

  it.each(["succeeded", "failed", "cancelled", "cleanup_failed"])(
    "does not offer cancellation for a later start with retained %s creation metadata",
    (status) => {
      expect(
        sandboxInstallCancellation({
          status: "starting",
          provisioning: { status, cancellationSupported: true },
        })
      ).toBe("hidden")
    }
  )

  it.each([
    "running",
    "stopped",
    "error",
    "cancelled",
    "restarting",
    "deleting",
  ])(
    "does not offer installation cancellation when sandbox is %s",
    (status) => {
      expect(sandboxInstallCancellation({ status })).toBe("hidden")
    }
  )

  it("formats Agent status and elapsed time", () => {
    expect(provisioningAgentToolStatusLabel("cached")).toBe("缓存复用")
    expect(provisioningAgentToolStatusLabel("running")).toBe("安装中")
    expect(provisioningAgentToolStatusLabel("cancelled")).toBe("已取消")
    expect(provisioningDuration(65_000)).toBe("1 分 5 秒")
  })
})
