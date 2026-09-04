import { describe, expect, it } from "vitest"

import {
  hasOnlyUnsupportedMCPAgentTools,
  isMCPValueReference,
  mcpValueFromVariable,
  mcpAgentToolCompatibility,
  mergeRequiredMCPVariableIds,
  missingMCPVariableIds,
  normalizeMCPArgs,
  normalizeMCPHeaders,
  unresolvedMCPHeaderReferences,
  unresolvedMCPValueReferences,
  unsafeLegacyMCPHeaderNames,
} from "./mcp-config"

describe("MCP configuration compatibility", () => {
  it("allows a mixed Agent selection when one client supports MCP", () => {
    expect(mcpAgentToolCompatibility(["codex", "pi"])).toEqual({
      supported: ["codex"],
      unsupported: ["pi"],
    })
    expect(hasOnlyUnsupportedMCPAgentTools(["codex", "pi"], ["docs"])).toBe(
      false
    )
  })

  it("blocks an MCP selection when every selected Agent is unsupported", () => {
    expect(hasOnlyUnsupportedMCPAgentTools(["pi", "kimi"], ["docs"])).toBe(true)
    expect(hasOnlyUnsupportedMCPAgentTools([], ["docs"])).toBe(true)
    expect(hasOnlyUnsupportedMCPAgentTools(["pi"], [])).toBe(false)
  })
})

describe("MCP legacy migration", () => {
  it("turns a shell-like legacy argument string into explicit arguments", () => {
    expect(normalizeMCPArgs(`--mode stdio --label "two words" ''`)).toEqual([
      "--mode",
      "stdio",
      "--label",
      "two words",
      "",
    ])
    expect(normalizeMCPArgs(["--mode", "stdio"])).toEqual(["--mode", "stdio"])
  })

  it("migrates reference headers without retaining legacy plaintext", () => {
    const legacy = [
      "Authorization=secret://MCP_TOKEN",
      "X-Tenant=env://TENANT_ID",
      "X-Unsafe=plaintext-secret",
    ].join("\n")
    expect(normalizeMCPHeaders(legacy)).toEqual([
      { name: "Authorization", valueFrom: "secret://MCP_TOKEN" },
      { name: "X-Tenant", valueFrom: "env://TENANT_ID" },
      { name: "X-Unsafe", valueFrom: "" },
    ])
    expect(unsafeLegacyMCPHeaderNames(legacy)).toEqual(["X-Unsafe"])
  })

  it("accepts only explicit environment or secret references", () => {
    expect(isMCPValueReference("env://TENANT_ID")).toBe(true)
    expect(isMCPValueReference("secret://MCP_TOKEN")).toBe(true)
    expect(isMCPValueReference("Bearer plaintext")).toBe(false)
    expect(isMCPValueReference("secret://invalid-name")).toBe(false)
  })

  it("references the injected Variable key instead of its Worker-side source", () => {
    expect(
      mcpValueFromVariable({
        key: "MCP_TOKEN",
        mode: "secret-ref",
        reference: "secret://HOST_FILE_NAME",
      })
    ).toBe("secret://MCP_TOKEN")
    expect(
      mcpValueFromVariable({
        key: "TENANT_ID",
        mode: "value-ref",
        reference: "env://HOST_TENANT",
      })
    ).toBe("env://TENANT_ID")
  })

  it("finds Header Variables that must be bound with a selected MCP", () => {
    const resources = [
      {
        id: "token",
        kind: "variable",
        enabled: true,
        spec: {
          key: "MCP_TOKEN",
          mode: "secret-ref",
          reference: "secret://HOST_FILE_NAME",
        },
      },
      {
        id: "docs",
        kind: "mcp",
        enabled: true,
        spec: {
          headers: [{ name: "Authorization", valueFrom: "secret://MCP_TOKEN" }],
        },
      },
    ]
    expect(missingMCPVariableIds(["docs"], [], resources)).toEqual(["token"])
    expect(missingMCPVariableIds(["docs"], ["token"], resources)).toEqual([])
    expect(
      mergeRequiredMCPVariableIds(["docs"], ["existing"], resources)
    ).toEqual(["existing", "token"])
    expect(unresolvedMCPHeaderReferences(["docs"], resources)).toEqual([])
    expect(unresolvedMCPHeaderReferences(["docs"], resources.slice(1))).toEqual(
      ["secret://MCP_TOKEN"]
    )
    expect(
      unresolvedMCPValueReferences(
        [{ name: "Authorization", valueFrom: "secret://MCP_TOKEN" }],
        resources
      )
    ).toEqual([])
    expect(
      unresolvedMCPValueReferences(
        [{ name: "X-Tenant", valueFrom: "env://TENANT_ID" }],
        resources
      )
    ).toEqual(["env://TENANT_ID"])
  })
})
