import { describe, expect, it } from "vitest"

import {
  environmentVariableEntries,
  environmentVariablesError,
} from "./environment-variables"

describe("environment variables", () => {
  it("keeps valid name and value pairs", () => {
    expect(
      environmentVariableEntries([{ name: "NODE_ENV", value: "production" }])
    ).toEqual([{ name: "NODE_ENV", value: "production" }])
    expect(
      environmentVariablesError([{ name: "NODE_ENV", value: "production" }])
    ).toBe("")
  })

  it("rejects duplicate, reserved, and malformed names", () => {
    expect(
      environmentVariablesError([
        { name: "PORT", value: "3000" },
        { name: "PORT", value: "4000" },
      ])
    ).toContain("重复")
    expect(
      environmentVariablesError([{ name: "AGENTBOX_KEY", value: "value" }])
    ).toContain("保留")
    expect(
      environmentVariablesError([{ name: "bad-name", value: "value" }])
    ).toContain("格式")
  })
})
