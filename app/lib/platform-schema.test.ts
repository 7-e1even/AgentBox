import { describe, expect, expectTypeOf, it } from "vitest"

import {
  resourceInputSchema,
  resourceSchema,
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
  skill: { source: "inline", instructions: "Example" },
  mcp: { transport: "stdio", command: "npx", args: "-y example" },
  sandbox: { serverId, runtimeId: "example-template" },
  variable: { key: "TOKEN", reference: "secret:example" },
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
    ["variable", { reference: "secret:example" }],
  ])("requires complete %s desired fields", (kind, spec) => {
    expect(resourceInputSchema.safeParse({ ...base, kind, spec }).success).toBe(
      false
    )
  })

  it.each([
    ["runtime", "cpu", 2],
    ["runtime", "memory", 4096],
    ["runtime", "desktop", "true"],
    ["runtime", "credentialIds", "credential-one"],
    ["runtime", "modelBindings", { credential: 42 }],
    ["runtime", "environmentVariables", [{ name: "TOKEN", value: 42 }]],
    ["mcp", "args", ["--stdio"]],
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
    expectTypeOf<MCPInput["spec"]["args"]>().toEqualTypeOf<string | undefined>()
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
})
