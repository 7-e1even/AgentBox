import { describe, expect, it } from "vitest"

import type { Resource, ResourceOfKind } from "./platform-schema"
import {
  capabilityUsage,
  capabilityUsageLabel,
  sandboxCapabilitySelection,
  sandboxConfigurationState,
} from "./resource-capabilities"

const timestamp = "2026-09-04T00:00:00Z"

function resource(
  kind: "runtime" | "sandbox",
  id: string,
  spec: Record<string, unknown>,
  generation = 1,
  observedGeneration = generation
) {
  return {
    id,
    kind,
    projectId: "default",
    name: id,
    description: "",
    enabled: true,
    specVersion: 1,
    generation,
    observedGeneration,
    createdAt: timestamp,
    updatedAt: timestamp,
    spec,
  } as Resource
}

describe("resource capability state", () => {
  it("counts direct Runtime and Sandbox consumers separately", () => {
    const resources = [
      resource("runtime", "template", { skillIds: ["review"] }),
      resource("sandbox", "sandbox-a", { skillIds: ["review"] }),
      resource("sandbox", "sandbox-b", { skillIds: ["other"] }),
    ]
    const usage = capabilityUsage(resources, "review", "skillIds")
    expect(usage).toEqual({ templates: 1, sandboxes: 1 })
    expect(capabilityUsageLabel(usage)).toBe("1 个模板 · 1 个沙箱")
  })

  it("uses the Sandbox snapshot and falls back only for legacy records", () => {
    const template = resource("runtime", "template", {
      skillIds: ["template-skill"],
      variableIds: ["template-variable"],
    }) as ResourceOfKind<"runtime">
    const sandbox = resource("sandbox", "sandbox", {
      skillIds: ["sandbox-skill"],
    }) as ResourceOfKind<"sandbox">
    expect(sandboxCapabilitySelection(sandbox, template, "skillIds")).toEqual({
      ids: ["sandbox-skill"],
      legacyTemplateFallback: false,
    })
    const legacy = resource(
      "sandbox",
      "legacy",
      {}
    ) as ResourceOfKind<"sandbox">
    expect(sandboxCapabilitySelection(legacy, template, "skillIds")).toEqual({
      ids: ["template-skill"],
      legacyTemplateFallback: true,
    })
    expect(sandboxCapabilitySelection(legacy, template, "variableIds")).toEqual(
      {
        ids: ["template-variable"],
        legacyTemplateFallback: true,
      }
    )
  })

  it("distinguishes applied, applying, and restart-required generations", () => {
    expect(
      sandboxConfigurationState(
        resource(
          "sandbox",
          "applied",
          { status: "running" },
          2,
          2
        ) as ResourceOfKind<"sandbox">
      )
    ).toBe("applied")
    expect(
      sandboxConfigurationState(
        resource(
          "sandbox",
          "applying",
          { status: "restarting" },
          3,
          2
        ) as ResourceOfKind<"sandbox">
      )
    ).toBe("applying")
    expect(
      sandboxConfigurationState(
        resource(
          "sandbox",
          "pending",
          { status: "running" },
          3,
          2
        ) as ResourceOfKind<"sandbox">
      )
    ).toBe("restart-required")
    expect(
      sandboxConfigurationState(
        resource(
          "sandbox",
          "explicit-pending",
          { status: "running", capabilitiesPendingRestart: true },
          2,
          2
        ) as ResourceOfKind<"sandbox">
      )
    ).toBe("restart-required")
    expect(
      sandboxConfigurationState(
        resource(
          "sandbox",
          "stale-generation",
          { status: "running", capabilitiesPendingRestart: false },
          3,
          2
        ) as ResourceOfKind<"sandbox">
      )
    ).toBe("restart-required")
  })
})
