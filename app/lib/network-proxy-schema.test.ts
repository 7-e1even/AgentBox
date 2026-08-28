import { describe, expect, it } from "vitest"

import { networkProxyCheckResponseSchema } from "./network-proxy-schema"

describe("network proxy check response", () => {
  it("accepts a successful latency result", () => {
    expect(
      networkProxyCheckResponseSchema.parse({
        result: {
          checkId: "754f76dd-2297-44e9-8204-a688be9be4a5",
          proxyId: "office-proxy",
          serverId: "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
          serverName: "Worker One",
          scope: "worker",
          status: "completed",
          ok: true,
          latencyMs: 128,
          target: "https://www.gstatic.com/generate_204",
          statusCode: 204,
          checkedAt: "2026-08-27T10:00:00Z",
        },
      }).result
    ).toMatchObject({ ok: true, latencyMs: 128, statusCode: 204 })
  })

  it("accepts a failed result without an HTTP status", () => {
    expect(
      networkProxyCheckResponseSchema.parse({
        result: {
          checkId: "754f76dd-2297-44e9-8204-a688be9be4a5",
          proxyId: "office-proxy",
          serverId: "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
          serverName: "Worker One",
          scope: "worker",
          status: "completed",
          ok: false,
          latencyMs: 10000,
          target: "https://www.gstatic.com/generate_204",
          error: "Worker 代理检测超时（10 秒）",
          checkedAt: "2026-08-27T10:00:10Z",
        },
      }).result.error
    ).toBe("Worker 代理检测超时（10 秒）")
  })

  it("accepts a pending Worker result without a synthetic outcome", () => {
    expect(
      networkProxyCheckResponseSchema.parse({
        result: {
          checkId: "754f76dd-2297-44e9-8204-a688be9be4a5",
          proxyId: "office-proxy",
          serverId: "7b20f83b-6418-4a9f-8477-3dc7c35d6310",
          serverName: "Worker One",
          scope: "worker",
          status: "pending",
          target: "https://www.gstatic.com/generate_204",
        },
      }).result.status
    ).toBe("pending")
  })
})
