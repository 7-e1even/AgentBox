import { describe, expect, it } from "vitest"

import {
  agentToolNeedsUpdate,
  compareSemanticVersions,
  sandboxAgentToolOperation,
  sandboxAgentToolStates,
} from "./sandbox-agent-tools"

describe("sandbox Agent tool state", () => {
  it("parses persisted versions and active operations", () => {
    const checkedAt = "2026-08-29T08:00:00Z"
    const spec = {
      agentToolVersions: [
        {
          tool: "codex",
          currentVersion: "0.1.0",
          latestVersion: "0.2.0",
          status: "installed",
          checkedAt,
        },
      ],
      agentToolOperation: {
        action: "update",
        status: "running",
        toolIds: ["codex"],
        message: "正在更新",
        startedAt: checkedAt,
        updatedAt: checkedAt,
      },
    }

    expect(sandboxAgentToolStates(spec)).toHaveLength(1)
    expect(sandboxAgentToolOperation(spec)?.status).toBe("running")
  })

  it("rejects unknown tool state instead of trusting persisted JSON", () => {
    expect(
      sandboxAgentToolStates({
        agentToolVersions: [
          {
            tool: "unknown",
            status: "installed",
            checkedAt: "2026-08-29T08:00:00Z",
          },
        ],
      })
    ).toEqual([])
  })
})

describe("semantic version comparison", () => {
  it("compares releases and prereleases", () => {
    expect(compareSemanticVersions("1.2.3", "1.2.4")).toBe(-1)
    expect(compareSemanticVersions("1.2.3-beta.2", "1.2.3-beta.10")).toBe(-1)
    expect(compareSemanticVersions("1.2.3", "1.2.3-beta.10")).toBe(1)
  })

  it("marks missing, broken, and older tools as actionable", () => {
    const checkedAt = "2026-08-29T08:00:00Z"
    expect(
      agentToolNeedsUpdate({
        tool: "codex",
        currentVersion: "0.1.0",
        latestVersion: "0.2.0",
        previousVersion: "",
        status: "installed",
        message: "",
        source: "npm",
        checkedAt,
      })
    ).toBe(true)
    expect(
      agentToolNeedsUpdate({
        tool: "codex",
        currentVersion: "",
        latestVersion: "0.2.0",
        previousVersion: "",
        status: "not-installed",
        message: "",
        source: "npm",
        checkedAt,
      })
    ).toBe(true)
  })
})
