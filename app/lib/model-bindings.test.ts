import { describe, expect, it } from "vitest"

import type { ManagedCredential } from "./credential-schema"
import { reconcileModelBindings } from "./model-bindings"

const timestamp = "2026-08-21T00:00:00Z"

function credential(
  id: string,
  modelIds: string[],
  enabled = true
): ManagedCredential {
  return {
    id,
    name: id,
    providerId: "custom",
    protocol: "openai-chat",
    endpoint: "https://example.com/v1",
    modelId: "",
    models: modelIds.map((modelId) => ({
      id: modelId,
      name: modelId,
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

describe("reconcileModelBindings", () => {
  it("automatically binds a credential with one available model", () => {
    expect(
      reconcileModelBindings(["kimi"], [credential("kimi", ["k2p5"])])
    ).toEqual({ kimi: "k2p5" })
  })

  it("preserves valid choices without guessing between multiple models", () => {
    const credentials = [credential("openai", ["gpt-5", "gpt-5-mini"])]

    expect(
      reconcileModelBindings(["openai"], credentials, {
        openai: "gpt-5-mini",
      })
    ).toEqual({ openai: "gpt-5-mini" })
    expect(reconcileModelBindings(["openai"], credentials)).toEqual({})
  })

  it("replaces a stale choice when only one current model remains", () => {
    expect(
      reconcileModelBindings(["kimi"], [credential("kimi", ["k2p5"])], {
        kimi: "removed-model",
      })
    ).toEqual({ kimi: "k2p5" })
  })

  it("removes unselected, unavailable, and disabled credential bindings", () => {
    expect(
      reconcileModelBindings(
        ["openai", "disabled", "missing"],
        [
          credential("openai", ["gpt-5", "gpt-5-mini"]),
          credential("disabled", ["model-one"], false),
        ],
        {
          openai: "gpt-5",
          disabled: "model-one",
          extra: "model-extra",
        }
      )
    ).toEqual({ openai: "gpt-5" })
  })
})
