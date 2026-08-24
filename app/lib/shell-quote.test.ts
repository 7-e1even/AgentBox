import { describe, expect, it } from "vitest"

import { shellSingleQuote } from "./shell-quote"

describe("shellSingleQuote", () => {
  it("keeps ordinary values in one shell word", () => {
    expect(shellSingleQuote("secret value")).toBe("'secret value'")
  })

  it("escapes embedded single quotes", () => {
    expect(shellSingleQuote("secret'value")).toBe("'secret'\"'\"'value'")
  })
})
