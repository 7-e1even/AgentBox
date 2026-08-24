import { describe, expect, it } from "vitest"

import {
  automationInputSchema,
  automationRunSchema,
  automationSchema,
} from "./automation-schema"

const timestamp = "2026-08-18T00:00:00Z"

describe("automation schemas", () => {
  it("parses a template-based sandbox automation", () => {
    const automation = automationSchema.parse({
      id: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388",
      projectId: "default",
      name: "PR Preview",
      description: "",
      enabled: true,
      trigger: { type: "webhook", authMode: "bearer" },
      templateId: "runtime-one",
      modelBindings: { "credential-one": "model-one" },
      endpointId: "75778270-bdbf-4e2f-bbeb-b3133447a367",
      secretLastFour: "test",
      createdBy: null,
      updatedBy: null,
      lastTriggeredAt: null,
      secretRotatedAt: timestamp,
      createdAt: timestamp,
      updatedAt: timestamp,
    })

    expect(automation.templateId).toBe("runtime-one")
    expect(automation.modelBindings).toEqual({
      "credential-one": "model-one",
    })
  })

  it("requires concrete model bindings in automation input", () => {
    const result = automationInputSchema.safeParse({
      projectId: "default",
      name: "PR Preview",
      description: "",
      enabled: true,
      trigger: { type: "webhook", authMode: "bearer" },
      templateId: "runtime-one",
    })

    expect(result.success).toBe(false)
  })

  it("rejects multiline custom webhook secrets", () => {
    const result = automationInputSchema.safeParse({
      projectId: "default",
      name: "PR Preview",
      description: "",
      enabled: true,
      secret: "long-enough-secret\nsecond-line",
      trigger: { type: "webhook", authMode: "bearer" },
      templateId: "runtime-one",
      modelBindings: { "credential-one": "model-one" },
    })

    expect(result.success).toBe(false)
  })

  it("rejects legacy execution actions", () => {
    const result = automationInputSchema.safeParse({
      projectId: "default",
      name: "Codex task",
      description: "",
      enabled: true,
      trigger: { type: "webhook", authMode: "bearer" },
      templateId: "runtime-one",
      modelBindings: {},
      action: { type: "run-codex", templateId: "runtime-one" },
    })

    expect(result.success).toBe(false)
  })

  it("keeps old run records readable with canonical defaults", () => {
    const run = automationRunSchema.parse({
      id: "96b47a49-96d4-492d-85e6-8c14f0315ae8",
      automationId: null,
      projectId: "default",
      automationName: "PR Preview",
      templateId: "runtime-one",
      templateName: "Runtime One",
      triggerSource: "webhook",
      authMode: "bearer",
      idempotencyFingerprint: "",
      payloadSha256: "00",
      payloadBytes: 2,
      inputSha256: "",
      status: "failed",
      sandboxId: null,
      workerJobId: null,
      errorCode: "input_invalid",
      errorMessage: "offline",
      receivedAt: timestamp,
      queuedAt: null,
      startedAt: null,
      finishedAt: timestamp,
    })

    expect(run.event.source).toBe("generic")
    expect(run.provisioning.stage).toBe("")
    expect(run.provisioning.timings).toEqual([])
  })
})
