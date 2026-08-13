import { describe, expect, it } from "vitest"

import { managedCredentialSchema } from "./credential-schema"

const credential = {
  id: "openai-primary",
  name: "OpenAI API Key",
  providerId: "openai",
  protocol: "openai-responses" as const,
  endpoint: "https://api.openai.com/v1",
  modelId: "gpt-5.3-codex",
  models: [
    {
      id: "gpt-5.3-codex",
      name: "GPT-5.3 Codex",
      group: "openai",
      source: "remote" as const,
    },
  ],
  maskedSecret: "••••1234",
  enabled: true,
  lastCheckAt: null,
  lastCheckOk: null,
  lastCheckError: "",
  createdAt: "2026-08-13T09:00:00Z",
  updatedAt: "2026-08-13T09:00:00Z",
}

describe("managedCredentialSchema", () => {
  it("accepts a masked credential response", () => {
    expect(managedCredentialSchema.parse(credential)).toEqual(credential)
  })

  it("rejects an API response that leaks a plaintext secret", () => {
    expect(
      managedCredentialSchema.safeParse({ ...credential, secret: "sk-secret" })
        .success
    ).toBe(false)
  })
})
