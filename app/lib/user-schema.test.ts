import { describe, expect, it } from "vitest"

import { userInputSchema, userSchema } from "./user-schema"

describe("user schemas", () => {
  it("accepts a persisted administrator", () => {
    expect(
      userSchema.parse({
        id: "7250fd43-8301-44d2-a03f-df4dcc65e499",
        name: "AgentBox Admin",
        username: "admin",
        email: "admin@agentbox.local",
        role: "admin",
        status: "active",
        lastLoginAt: null,
        createdAt: "2026-08-13T12:00:00Z",
        updatedAt: "2026-08-13T12:00:00Z",
      })
    ).toMatchObject({
      role: "admin",
      status: "active",
      preferences: {
        successNotifications: true,
        density: "comfortable",
        showCapabilities: true,
        showInfrastructure: true,
        showGovernance: true,
      },
    })
  })

  it("rejects a short password", () => {
    expect(() =>
      userInputSchema.parse({
        name: "Viewer",
        username: "viewer",
        email: "viewer@example.com",
        password: "short",
        role: "viewer",
        status: "active",
      })
    ).toThrow()
  })

  it("normalizes a username and rejects an email address", () => {
    expect(
      userInputSchema.parse({
        name: "Viewer",
        username: "  Viewer.One  ",
        email: "viewer@example.com",
        password: "password123",
        role: "viewer",
        status: "active",
      }).username
    ).toBe("viewer.one")

    expect(() =>
      userInputSchema.parse({
        name: "Viewer",
        username: "viewer@example.com",
        email: "viewer@example.com",
        password: "password123",
        role: "viewer",
        status: "active",
      })
    ).toThrow()
  })
})
