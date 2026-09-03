import { describe, expect, it } from "vitest"

import type { Resource } from "./platform-schema"
import {
  extensionMatchesQuery,
  extensionSelectionOptions,
  filterExtensionSelectionOptions,
  sandboxExtensions,
  type SandboxExtension,
} from "./sandbox-extensions"

function extension(id: string, enabled = true): SandboxExtension {
  return {
    id,
    kind: "extension",
    projectId: "project-one",
    name: id === "multica-cli" ? "Multica 客户端" : "Git 工具",
    description: id === "multica-cli" ? "连接执行服务" : "拉取代码仓库",
    enabled,
    specVersion: 1,
    generation: 1,
    observedGeneration: 0,
    createdAt: "2026-08-31T00:00:00Z",
    updatedAt: "2026-08-31T00:00:00Z",
    spec: {
      version: "1.0.0",
      source: "custom",
      installScript: "true",
      verifyScript: "true",
      timeoutSeconds: 600,
      requiresNetwork: true,
    },
  }
}

describe("sandbox extension selection", () => {
  it("searches names, IDs and descriptions without case or surrounding-space sensitivity", () => {
    const item = extension("multica-cli")
    expect(extensionMatchesQuery(item, " MULTICA-CLI ")).toBe(true)
    expect(extensionMatchesQuery(item, "客户端")).toBe(true)
    expect(extensionMatchesQuery(item, "执行服务")).toBe(true)
    expect(extensionMatchesQuery(item, "git")).toBe(false)
  })

  it("keeps disabled and unavailable selections visible and removable", () => {
    const disabled = extension("multica-cli", false)
    const selected = ["multica-cli", "removed-extension", "removed-extension"]
    const options = extensionSelectionOptions([disabled], selected)
    expect(options.map((option) => option.id)).toEqual([
      "multica-cli",
      "removed-extension",
    ])
    expect(options[0].extension?.enabled).toBe(false)
    expect(options[1].extension).toBeUndefined()
    expect(selected).toEqual([
      "multica-cli",
      "removed-extension",
      "removed-extension",
    ])
  })

  it("preserves all selections across searches and selected-only views", () => {
    const selected = ["multica-cli", "missing-tool"]
    const options = extensionSelectionOptions(
      [extension("multica-cli"), extension("git-cli")],
      selected
    )
    const searchResult = filterExtensionSelectionOptions(
      options,
      selected,
      "仓库",
      false
    )
    expect(searchResult.map((item) => item.id)).toEqual(["git-cli"])
    expect(
      filterExtensionSelectionOptions(options, selected, "仓库", true)
    ).toEqual([])
    expect(
      filterExtensionSelectionOptions(options, selected, "", true).map(
        (item) => item.id
      )
    ).toEqual(selected)
    expect(
      filterExtensionSelectionOptions(
        options,
        selected,
        " MISSING-TOOL ",
        true
      ).map((item) => item.id)
    ).toEqual(["missing-tool"])
    expect(selected).toEqual(["multica-cli", "missing-tool"])
  })

  it("limits the catalogue to extension resources without discarding disabled drafts", () => {
    const enabled = extension("multica-cli")
    const disabled = extension("git-cli", false)
    const project: Resource = {
      ...enabled,
      id: "project-one",
      kind: "project",
      projectId: null,
      spec: {},
    }
    expect(sandboxExtensions([project, enabled, disabled])).toEqual([
      enabled,
      disabled,
    ])
  })
})
