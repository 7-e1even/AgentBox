import { describe, expect, it } from "vitest"

import {
  agentInputSchema,
  agentResponseSchema,
  createSlug,
} from "./agent-schema"

const validAgent = {
  projectId: "default",
  runtimeId: "python-venv",
  name: "Research Agent",
  slug: "research-agent",
  description: "A focused research assistant.",
  avatar: "RA",
  providerId: "openai",
  modelId: "gpt-5",
  credentialId: "openai-primary",
  systemPrompt:
    "You research questions carefully and explain the evidence behind every conclusion.",
  skillIds: ["web-research"],
  mcpServerIds: ["browser"],
  variableIds: ["github-token"],
  customArgs: [],
  temperature: 0.4,
  maxSteps: 12,
  concurrency: 1,
  sandboxPolicy: "new" as const,
  status: "draft" as const,
}

describe("agentInputSchema", () => {
  it("accepts a complete agent declaration", () => {
    expect(agentInputSchema.parse(validAgent)).toEqual(validAgent)
  })

  it("rejects incomplete system instructions", () => {
    const result = agentInputSchema.safeParse({
      ...validAgent,
      systemPrompt: "too short",
    })
    expect(result.success).toBe(false)
  })

  it("rejects invalid slugs and unsafe step limits", () => {
    expect(
      agentInputSchema.safeParse({ ...validAgent, slug: "Bad Slug" }).success
    ).toBe(false)
    expect(
      agentInputSchema.safeParse({ ...validAgent, maxSteps: 100 }).success
    ).toBe(false)
  })
})

describe("createSlug", () => {
  it("normalizes supported names and provides a fallback", () => {
    expect(createSlug("  Release Writer  ")).toBe("release-writer")
    expect(createSlug("研究助手")).toBe("agent")
  })
})

describe("agentResponseSchema", () => {
  it("validates the Go API response boundary", () => {
    const response = {
      agent: {
        ...validAgent,
        id: "b88eb8db-3954-4d1a-bda3-005e1fb375c4",
        version: 1,
        createdAt: "2026-08-13T05:00:00Z",
        updatedAt: "2026-08-13T05:00:00Z",
      },
    }
    expect(agentResponseSchema.parse(response)).toEqual(response)
    expect(
      agentResponseSchema.safeParse({
        ...response,
        agent: { ...response.agent, version: 0 },
      }).success
    ).toBe(false)
  })
})
