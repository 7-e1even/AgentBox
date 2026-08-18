import { describe, expect, it } from "vitest"

import { automationRunSchema, automationSchema } from "./automation-schema"

const timestamp = "2026-08-18T00:00:00Z"

describe("automation schemas", () => {
  it("keeps old automation records readable during rolling upgrades", () => {
    const automation = automationSchema.parse({
      id: "5f7a65c5-1df2-4ac3-bdbf-753af92ac388",
      projectId: "default",
      name: "PR Preview",
      description: "",
      enabled: true,
      trigger: { type: "webhook", authMode: "bearer" },
      action: {
        type: "create-sandbox",
        templateId: "runtime-one",
        modelBindings: {},
        inputTemplate: "{}",
      },
      endpointId: "75778270-bdbf-4e2f-bbeb-b3133447a367",
      secretLastFour: "test",
      createdBy: null,
      updatedBy: null,
      lastTriggeredAt: null,
      secretRotatedAt: timestamp,
      createdAt: timestamp,
      updatedAt: timestamp,
    })

    expect(automation.conditionTemplate).toBe("true")
    expect(automation.action.timeoutSeconds).toBe(900)
    expect(automation.action.cleanupPolicy).toBe("never")
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

    expect(run.actionType).toBe("create-sandbox")
    expect(run.event.source).toBe("generic")
    expect(run.output).toBe("")
  })
})
