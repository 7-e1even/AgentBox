import { describe, expect, expectTypeOf, it } from "vitest"

import {
  resourceInputSchema,
  resourceSchema,
  normalizeVariableSpec,
  sandboxSpecForUpdate,
  type ResourceInput,
  type ResourceKind,
} from "./platform-schema"

const serverId = "caab4b00-6966-449e-8c96-d1edff8e40b6"
const timestamp = "2026-08-31T00:00:00Z"
const base = {
  id: "example-resource",
  name: "Example resource",
  projectId: "default",
  description: "",
  enabled: true,
}
const validSpecs: Record<ResourceKind, object> = {
  project: { emoji: "📁" },
  image: { reference: "ubuntu:24.04", architecture: "all", modes: ["docker"] },
  runtime: { serverId, driver: "docker", imageReference: "ubuntu:24.04" },
  skill: {
    source: "inline",
    instructions:
      "---\nname: example-resource\ndescription: Example skill\n---\nDo the work.\n",
  },
  mcp: { transport: "stdio", command: "npx", args: ["-y", "example"] },
  sandbox: { serverId, runtimeId: "example-template" },
  variable: {
    key: "TOKEN",
    mode: "secret-ref",
    reference: "secret://SOURCE_TOKEN",
  },
  extension: {
    version: "1.0.0",
    installScript: "touch /tmp/extension-ready",
    verifyScript: "test -f /tmp/extension-ready",
  },
}

describe("typed resource contracts", () => {
  it.each(Object.entries(validSpecs))(
    "parses %s desired input",
    (kind, spec) => {
      expect(resourceInputSchema.parse({ ...base, kind, spec })).toMatchObject({
        ...base,
        kind,
        spec,
        specVersion: 1,
      })
    }
  )

  it.each([
    ["image", { architecture: "all", modes: ["docker"] }],
    ["image", { reference: "ubuntu:24.04", architecture: "all", modes: [] }],
    ["runtime", { driver: "docker", imageReference: "ubuntu:24.04" }],
    ["runtime", { serverId, imageReference: "ubuntu:24.04" }],
    ["runtime", { serverId, driver: "docker" }],
    ["sandbox", { serverId }],
    ["sandbox", { runtimeId: "example-template" }],
    ["mcp", { transport: "stdio" }],
    ["mcp", { transport: "http", command: "ignored" }],
    ["variable", { key: "TOKEN" }],
    ["variable", { reference: "secret://SOURCE_TOKEN" }],
  ])("requires complete %s desired fields", (kind, spec) => {
    expect(resourceInputSchema.safeParse({ ...base, kind, spec }).success).toBe(
      false
    )
  })

  it.each([
    { key: "bad-key", mode: "value-ref", reference: "env://SOURCE" },
    { key: "AGENTBOX_TOKEN", mode: "value-ref", reference: "env://SOURCE" },
    { key: "IS_SANDBOX", mode: "value-ref", reference: "env://SOURCE" },
    { key: "TOKEN", mode: "value-ref", reference: "secret://SOURCE" },
    { key: "TOKEN", mode: "secret-ref", reference: "env://SOURCE" },
    { key: "TOKEN", mode: "secret-ref", reference: "plaintext" },
    { key: "A".repeat(129), mode: "value-ref", reference: "env://SOURCE" },
    { key: "TOKEN", mode: "value-ref", reference: `env://${"A".repeat(129)}` },
  ])("rejects invalid Variable contract %#", (spec) => {
    expect(
      resourceInputSchema.safeParse({ ...base, kind: "variable", spec }).success
    ).toBe(false)
  })

  it("canonicalizes a legacy Variable mode from its reference scheme", () => {
    expect(
      resourceInputSchema.parse({
        ...base,
        kind: "variable",
        spec: { key: "TOKEN", reference: "env://SOURCE_TOKEN" },
      }).spec
    ).toEqual({
      key: "TOKEN",
      mode: "value-ref",
      reference: "env://SOURCE_TOKEN",
    })
    expect(
      normalizeVariableSpec({
        key: "TOKEN",
        mode: " SECRET-REF ",
        reference: "secret://SOURCE_TOKEN",
      })
    ).toEqual({
      key: "TOKEN",
      mode: "secret-ref",
      reference: "secret://SOURCE_TOKEN",
    })
  })

  it("rejects HTTP MCP URLs containing query parameters", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.test/api?token=plaintext",
        },
      }).success
    ).toBe(false)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.test/api?",
        },
      }).success
    ).toBe(false)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.test/api%3Fname",
        },
      }).success
    ).toBe(true)
  })

  it.each([
    ["runtime", "cpu", 2],
    ["runtime", "memory", 4096],
    ["runtime", "desktop", "true"],
    ["runtime", "credentialIds", "credential-one"],
    ["runtime", "modelBindings", { credential: 42 }],
    ["runtime", "environmentVariables", [{ name: "TOKEN", value: 42 }]],
    ["mcp", "args", "--stdio"],
    ["skill", "instructions", false],
  ] as const)("rejects an incorrect %s.%s type", (kind, key, value) => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind,
        spec: { ...validSpecs[kind], [key]: value },
      }).success
    ).toBe(false)
  })

  it("rejects fields belonging to a different kind", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "project",
        spec: { serverId },
      }).success
    ).toBe(false)
  })

  it("strips response metadata, sandbox observations and provenance before saving", () => {
    const resource = resourceInputSchema.parse({
      ...base,
      kind: "sandbox",
      generation: 17,
      observedGeneration: 12,
      createdAt: timestamp,
      updatedAt: timestamp,
      spec: {
        ...validSpecs.sandbox,
        proxyId: "desired-proxy",
        status: "running",
        message: "Worker observation",
        externalId: "container-id",
        provisioning: {},
        appliedProxyId: "old-proxy",
        proxyOperation: {},
        agentToolVersions: [],
        agentToolOperation: {},
        extensionSnapshots: [],
        extensionStates: [],
        capabilitiesPendingRestart: true,
        capabilityDigest: "sha256:applied",
        capabilitiesAppliedAt: timestamp,
        runtimeModelSources: {
          primary: {
            credentialId: "backup",
            modelId: "model-next",
            updatedAt: timestamp,
          },
        },
        runtimeModelSourcesComplete: true,
        automationId: "automation-id",
        automationRunId: "run-id",
      },
    })
    expect(resource).not.toHaveProperty("generation")
    expect(resource).not.toHaveProperty("observedGeneration")
    expect(resource).not.toHaveProperty("createdAt")
    expect(resource.spec).toEqual({
      ...validSpecs.sandbox,
      proxyId: "desired-proxy",
    })
  })

  it.each(Object.keys(validSpecs))(
    "reads historical %s records with absent fields",
    (kind) => {
      expect(
        resourceSchema.parse({
          ...base,
          kind,
          spec: {},
          createdAt: timestamp,
          updatedAt: timestamp,
        })
      ).toMatchObject({
        spec: {},
        specVersion: 1,
        generation: 1,
        observedGeneration: 0,
      })
    }
  )

  it("keeps legacy vm and imageId readable without manufacturing a desktop setting", () => {
    const resource = resourceSchema.parse({
      ...base,
      kind: "runtime",
      spec: { driver: "vm", imageId: "legacy-image" },
      createdAt: timestamp,
      updatedAt: timestamp,
    })
    expect(resource.spec).toEqual({ driver: "vm", imageId: "legacy-image" })
    expect(resource.spec).not.toHaveProperty("desktop")
  })

  it("accepts nullish sandbox observations while retaining their declared types", () => {
    const resource = resourceSchema.parse({
      ...base,
      kind: "sandbox",
      createdAt: timestamp,
      updatedAt: timestamp,
      spec: {
        status: null,
        message: null,
        provisioning: null,
        proxyOperation: null,
        agentToolOperation: null,
        agentToolVersions: null,
        automationId: null,
      },
    })
    expect(resource.spec).toMatchObject({ status: null, provisioning: null })
    expect(
      resourceSchema.safeParse({
        ...resource,
        spec: { status: 42 },
      }).success
    ).toBe(false)
  })

  it.each([0, 2, "1"])(
    "rejects unsupported specVersion %s on input and response",
    (specVersion) => {
      const resource = { ...base, kind: "project", spec: {}, specVersion }
      expect(resourceInputSchema.safeParse(resource).success).toBe(false)
      expect(
        resourceSchema.safeParse({
          ...resource,
          createdAt: timestamp,
          updatedAt: timestamp,
        }).success
      ).toBe(false)
    }
  )

  it("keeps string-valued execution fields and narrows specs by kind", () => {
    type RuntimeInput = Extract<ResourceInput, { kind: "runtime" }>
    expectTypeOf<RuntimeInput["spec"]["cpu"]>().toEqualTypeOf<
      string | undefined
    >()
    expectTypeOf<RuntimeInput["spec"]["memory"]>().toEqualTypeOf<
      string | undefined
    >()
    type MCPInput = Extract<ResourceInput, { kind: "mcp" }>
    expectTypeOf<MCPInput["spec"]["args"]>().toEqualTypeOf<
      string[] | undefined
    >()
    type SandboxInput = Extract<ResourceInput, { kind: "sandbox" }>
    expectTypeOf<SandboxInput["spec"]["runtimeId"]>().toEqualTypeOf<string>()
  })

  it("preserves existing sandbox creation fields and their absence when saving a draft", () => {
    const current = {
      serverId,
      runtimeId: "original-template",
      cpu: "2",
      proxyId: "original-proxy",
    }
    const draft = {
      runtimeId: "display-template",
      serverId: "display-server",
      driver: "docker",
      imageReference: "ubuntu:24.04",
      imageId: "display-image",
      cpu: "4",
      memory: "8 GiB",
      desktop: false,
      network: "restricted",
      workdir: "/workspace",
      setup: "echo setup",
      extensionIds: ["new-template-extension"],
      workspace: "display-workspace",
      proxyId: "display-proxy",
      agentTools: ["codex"],
      modelBindings: { credential: "new-model" },
      environmentVariables: [{ name: "EXAMPLE", value: "new value" }],
    }
    expect(sandboxSpecForUpdate(draft, current)).toEqual({
      ...current,
      agentTools: ["codex"],
      modelBindings: { credential: "new-model" },
      environmentVariables: [{ name: "EXAMPLE", value: "new value" }],
    })
    expect(draft.desktop).toBe(false)
    expect(current.cpu).toBe("2")
    expect(
      sandboxSpecForUpdate(
        { desktop: true, proxyId: "inherited" },
        { desktop: false }
      )
    ).toEqual({ desktop: false })
  })

  it("allows incomplete extension drafts but requires scripts before enabling", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "extension",
        enabled: false,
        spec: {},
      }).success
    ).toBe(true)
    expect(
      resourceInputSchema.safeParse({ ...base, kind: "extension", spec: {} })
        .success
    ).toBe(false)
  })

  it("limits extension scripts by UTF-8 bytes, including disabled drafts", () => {
    const draft = {
      ...base,
      kind: "extension",
      enabled: false,
      spec: { installScript: "中".repeat(21845) },
    }
    expect(resourceInputSchema.safeParse(draft).success).toBe(true)
    expect(
      resourceInputSchema.safeParse({
        ...draft,
        spec: { installScript: "中".repeat(21846) },
      }).success
    ).toBe(false)
  })

  it("uses the same Unicode version length limit as the API", () => {
    const spec = { ...validSpecs.extension, version: "🧩".repeat(64) }
    expect(
      resourceInputSchema.safeParse({ ...base, kind: "extension", spec })
        .success
    ).toBe(true)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "extension",
        spec: { ...spec, version: spec.version + "x" },
      }).success
    ).toBe(false)
  })

  it("keeps the original extension selection when saving an existing sandbox", () => {
    expect(
      sandboxSpecForUpdate(
        { extensionIds: ["new-extension"] },
        { extensionIds: ["original-extension"] }
      )
    ).toEqual({ extensionIds: ["original-extension"] })
    expect(
      sandboxSpecForUpdate({ extensionIds: ["new-extension"] }, {})
    ).not.toHaveProperty("extensionIds")
  })

  it("preserves legacy capability inheritance until that field is changed", () => {
    const inheritedDraft = {
      skillIds: ["template-skill"],
      mcpServerIds: ["template-mcp"],
      variableIds: ["template-variable"],
    }
    expect(sandboxSpecForUpdate(inheritedDraft, {})).toEqual({})
    expect(
      sandboxSpecForUpdate(inheritedDraft, {}, ["skillIds", "mcpServerIds"])
    ).toEqual({
      skillIds: ["template-skill"],
      mcpServerIds: ["template-mcp"],
    })
    expect(
      sandboxSpecForUpdate(inheritedDraft, {
        skillIds: ["existing-override"],
      })
    ).toEqual({ skillIds: ["template-skill"] })
  })

  it("reads legacy MCP strings while requiring canonical arrays on input", () => {
    const legacy = resourceSchema.parse({
      ...base,
      kind: "mcp",
      spec: {
        transport: "stdio",
        command: "npx",
        args: "-y example",
        headers: "Authorization=secret://MCP_TOKEN",
      },
      createdAt: timestamp,
      updatedAt: timestamp,
    })
    if (legacy.kind !== "mcp") throw new Error("expected MCP resource")
    expect(legacy.spec.args).toBe("-y example")
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: { transport: "stdio", command: "npx", args: "-y example" },
      }).success
    ).toBe(false)
  })

  it("accepts only structured MCP Header references", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.com",
          headers: [{ name: "Authorization", valueFrom: "secret://MCP_TOKEN" }],
        },
      }).success
    ).toBe(true)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.com",
          headers: [{ name: "Authorization", valueFrom: "Bearer plaintext" }],
        },
      }).success
    ).toBe(false)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "mcp",
        spec: {
          transport: "http",
          url: "https://mcp.example.com",
          headers: [
            { name: "Authorization", valueFrom: `secret://${"A".repeat(129)}` },
          ],
        },
      }).success
    ).toBe(false)
  })

  it("matches MCP transport and protocol safety constraints", () => {
    const parseMCP = (spec: Record<string, unknown>) =>
      resourceInputSchema.safeParse({ ...base, kind: "mcp", spec }).success

    expect(parseMCP({ transport: "stdio", command: "node", args: [""] })).toBe(
      false
    )
    expect(
      parseMCP({
        transport: "stdio",
        command: "node",
        url: "https://mcp.example.com",
      })
    ).toBe(false)
    expect(
      parseMCP({
        transport: "http",
        url: "https://user:password@mcp.example.com/path#fragment",
      })
    ).toBe(false)
    expect(
      parseMCP({
        transport: "http",
        url: "https://mcp.example.com",
        headers: [{ name: "Host", valueFrom: "env://MCP_HOST" }],
      })
    ).toBe(false)
  })

  it("validates canonical Skill identity and strips read-only summary fields", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "skill",
        spec: {
          ...validSpecs.skill,
          instructions:
            "---\nname: another-id\ndescription: Example skill\n---\nDo the work.\n",
        },
      }).success
    ).toBe(false)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "skill",
        spec: { ...validSpecs.skill, bundleDigest: "sha256:server-owned" },
      }).success
    ).toBe(false)
  })

  it("blocks only all-unsupported Agent and MCP combinations", () => {
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "runtime",
        spec: {
          ...validSpecs.runtime,
          agentTools: ["pi"],
          mcpServerIds: ["docs"],
        },
      }).success
    ).toBe(false)
    expect(
      resourceInputSchema.safeParse({
        ...base,
        kind: "runtime",
        spec: {
          ...validSpecs.runtime,
          agentTools: ["pi", "codex"],
          mcpServerIds: ["docs"],
        },
      }).success
    ).toBe(true)
  })
})
