import { describe, expect, it } from "vitest"

import type { ManagedCredential } from "./credential-schema"
import { runtimeModelSourcesSchema } from "./platform-schema"
import {
  describeModelSource,
  filterModelSourceOptions,
  findModelSourceOption,
  modelSourceOptions,
  runtimeModelSourceSlots,
  sameModelSource,
} from "./sandbox-model-sources"

const timestamp = "2026-09-03T08:00:00Z"

function credential(
  id: string,
  name: string,
  models: Array<{ id: string; name: string }>,
  enabled = true
): ManagedCredential {
  return {
    id,
    name,
    providerId: "custom",
    protocol: "openai-chat",
    endpoint: "https://example.com/v1",
    modelId: "",
    models: models.map((model) => ({
      ...model,
      group: "custom",
      source: "remote",
    })),
    maskedSecret: "••••test",
    enabled,
    lastCheckAt: null,
    lastCheckOk: null,
    lastCheckError: "",
    createdAt: timestamp,
    updatedAt: timestamp,
  }
}

describe("runtime model sources", () => {
  it("accepts the observed slot-to-source contract", () => {
    expect(
      runtimeModelSourcesSchema.parse({
        primary: {
          credentialId: "backup",
          modelId: "gpt-5.1",
          updatedAt: timestamp,
        },
      })
    ).toEqual({
      primary: {
        credentialId: "backup",
        modelId: "gpt-5.1",
        updatedAt: timestamp,
      },
    })
    expect(
      runtimeModelSourcesSchema.safeParse({
        primary: {
          credentialId: "backup",
          modelId: "",
          updatedAt: timestamp,
        },
      }).success
    ).toBe(false)
  })

  it("keeps the original credential id as the stable slot", () => {
    const credentials = [
      credential("primary", "主通道", [{ id: "old", name: "Old" }]),
      credential("backup", "备用源", [{ id: "new", name: "New" }]),
    ]
    const slots = runtimeModelSourceSlots(
      {
        primary: {
          credentialId: "backup",
          modelId: "new",
          updatedAt: timestamp,
        },
      },
      {
        credentialIds: ["primary"],
        modelBindings: { primary: "old" },
      },
      credentials
    )

    expect(slots).toEqual([
      {
        slotCredentialId: "primary",
        slotName: "主通道",
        source: {
          credentialId: "backup",
          modelId: "new",
          updatedAt: timestamp,
        },
        recorded: true,
      },
    ])
    expect(describeModelSource(slots[0].source, credentials)).toBe(
      "备用源 · New"
    )
  })

  it("falls back to saved bindings for legacy running sandboxes", () => {
    const credentials = [
      credential("primary", "主通道", [{ id: "old", name: "Old" }]),
    ]

    expect(
      runtimeModelSourceSlots(
        undefined,
        {
          credentialIds: ["primary"],
          modelBindings: { primary: "old" },
        },
        credentials
      )
    ).toEqual([
      {
        slotCredentialId: "primary",
        slotName: "主通道",
        source: {
          credentialId: "primary",
          modelId: "old",
          updatedAt: null,
        },
        recorded: false,
      },
    ])
  })

  it("uses only observed slots when the runtime snapshot is complete", () => {
    const credentials = [
      credential("primary", "主通道", [{ id: "old", name: "Old" }]),
      credential("new-slot", "尚未应用", [{ id: "new", name: "New" }]),
      credential("backup", "备用源", [{ id: "next", name: "Next" }]),
    ]

    expect(
      runtimeModelSourceSlots(
        {
          primary: {
            credentialId: "backup",
            modelId: "next",
            updatedAt: timestamp,
          },
        },
        {
          credentialIds: ["primary", "new-slot"],
          modelBindings: { primary: "old", "new-slot": "new" },
        },
        credentials,
        true
      ).map((slot) => slot.slotCredentialId)
    ).toEqual(["primary"])
  })

  it("merges saved slots with sparse legacy runtime observations", () => {
    const credentials = [
      credential("primary", "主通道", [{ id: "old", name: "Old" }]),
      credential("secondary", "第二通道", [{ id: "other", name: "Other" }]),
      credential("backup", "备用源", [{ id: "next", name: "Next" }]),
    ]

    const slots = runtimeModelSourceSlots(
      {
        primary: {
          credentialId: "backup",
          modelId: "next",
          updatedAt: timestamp,
        },
      },
      {
        credentialIds: ["primary", "secondary"],
        modelBindings: { primary: "old", secondary: "other" },
      },
      credentials,
      false
    )

    expect(slots).toHaveLength(2)
    expect(
      slots.find((slot) => slot.slotCredentialId === "primary")
    ).toMatchObject({
      source: { credentialId: "backup", modelId: "next" },
      recorded: true,
    })
    expect(
      slots.find((slot) => slot.slotCredentialId === "secondary")
    ).toMatchObject({ recorded: false })
  })

  it("builds searchable choices from enabled, unique models", () => {
    const options = modelSourceOptions([
      credential("one", "服务一", [
        { id: " model-a ", name: "Model A" },
        { id: "model-a", name: "Duplicate" },
      ]),
      credential("off", "已停用", [{ id: "model-b", name: "Model B" }], false),
    ])

    expect(options).toHaveLength(1)
    expect(options[0]).toMatchObject({
      credentialId: "one",
      credentialName: "服务一",
      modelId: "model-a",
      modelName: "Model A",
    })
    expect(
      findModelSourceOption(options, {
        credentialId: "one",
        modelId: "model-a",
      })
    ).toBe(options[0])
  })

  it("searches the complete catalog before the UI display limit", () => {
    const options = modelSourceOptions(
      Array.from({ length: 150 }, (_, index) =>
        credential(`service-${index}`, "同名服务", [
          { id: "shared-model", name: "Shared Model" },
        ])
      )
    )

    expect(options).toHaveLength(150)
    expect(
      filterModelSourceOptions(options, " service-149   SHARED-model ").map(
        (option) => option.credentialId
      )
    ).toEqual(["service-149"])
  })

  it("compares both the credential and model", () => {
    const current = { credentialId: "one", modelId: "model-a" }
    expect(sameModelSource(current, { ...current })).toBe(true)
    expect(
      sameModelSource(current, { credentialId: "two", modelId: "model-a" })
    ).toBe(false)
    expect(sameModelSource(current, null)).toBe(false)
  })
})
