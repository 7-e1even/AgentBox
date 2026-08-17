import { afterEach, describe, expect, it, vi } from "vitest"

import { errorMessage, requestJson } from "./api-client"

function jsonResponse(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

describe("requestJson", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("returns the parsed JSON body on success", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(200, { ok: 1 })))
    await expect(requestJson<{ ok: number }>("/api/x")).resolves.toEqual({
      ok: 1,
    })
  })

  it("sends a JSON content type header", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, {}))
    vi.stubGlobal("fetch", fetchMock)
    await requestJson("/api/x", { method: "POST", body: "{}" })
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/x",
      expect.objectContaining({
        headers: expect.objectContaining({
          "Content-Type": "application/json",
        }),
      })
    )
  })

  it("returns undefined for 204 responses", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(204)))
    await expect(requestJson<string>("/api/x")).resolves.toBeUndefined()
  })

  it("redirects to /login and throws on 401", async () => {
    const assign = vi.fn()
    vi.stubGlobal("window", { location: { assign } })
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(401)))
    await expect(requestJson("/api/x")).rejects.toThrow("登录状态已过期")
    expect(assign).toHaveBeenCalledWith("/login")
  })

  it("surfaces the server error message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(400, { error: "名称已被占用" }))
    )
    await expect(requestJson("/api/x")).rejects.toThrow("名称已被占用")
  })

  it("falls back to a permission message on 403 without a body error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(403)))
    await expect(requestJson("/api/x")).rejects.toThrow("没有权限执行此操作")
  })

  it("falls back to a generic message when the error body is not JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("<html>oops</html>", { status: 500 }))
    )
    await expect(requestJson("/api/x")).rejects.toThrow("请求失败")
  })
})

describe("errorMessage", () => {
  it("returns the message of Error instances", () => {
    expect(errorMessage(new Error("出错了"))).toBe("出错了")
  })

  it("returns a generic message for non-Error values", () => {
    expect(errorMessage("nope")).toBe("请稍后重试")
  })
})
