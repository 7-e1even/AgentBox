import { describe, expect, it } from "vitest"

import {
  networkProxyCheckResponseSchema,
  networkProxyInputSchema,
  sandboxProxyOperationSchema,
} from "./network-proxy-schema"

describe("network proxy input", () => {
  it.each(["http", "https", "socks5", "socks5h"] as const)(
    "accepts the %s protocol",
    (scheme) => {
      expect(
        networkProxyInputSchema.parse({
          id: "office-proxy",
          name: "Office proxy",
          scheme,
          host: "proxy.internal",
          port: 1080,
          username: "",
          password: "",
          noProxy: [],
          enabled: true,
        }).scheme
      ).toBe(scheme)
    }
  )
})

describe("sandbox proxy operation", () => {
  it("keeps desired and applied proxy state separate while a Worker job is queued", () => {
    expect(
      sandboxProxyOperationSchema.parse({
        status: "queued",
        desiredProxyId: "backup-proxy",
        appliedProxyId: "office-proxy",
        message: "正在排队应用网络出口",
        updatedAt: "2026-08-30T08:00:00Z",
      })
    ).toMatchObject({
      status: "queued",
      desiredProxyId: "backup-proxy",
      appliedProxyId: "office-proxy",
    })
  })
})

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
