import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  SandboxSessionClient,
  sandboxSessionTicketTimeoutMs,
} from "./sandbox-session"

class MockWebSocket {
  static OPEN = 1
  static instances: MockWebSocket[] = []

  readyState = MockWebSocket.OPEN
  sent: string[] = []
  closeCode: number | undefined
  private listeners = new Map<string, Set<(event: unknown) => void>>()

  constructor(public readonly url: string) {
    MockWebSocket.instances.push(this)
  }

  addEventListener(type: string, listener: (event: unknown) => void) {
    const set = this.listeners.get(type) ?? new Set()
    set.add(listener)
    this.listeners.set(type, set)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close(code = 1000) {
    if (code !== 1000 && (code < 3000 || code > 4999)) {
      throw new DOMException(
        "invalid WebSocket close code",
        "InvalidAccessError"
      )
    }
    this.closeCode = code
    this.readyState = 3
    this.emit("close", {})
  }

  emit(type: string, event: unknown) {
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }

  receive(message: unknown) {
    this.emit("message", { data: JSON.stringify(message) })
  }

  lastRequest(operation: string) {
    const sent = this.sent
      .map((raw) => JSON.parse(raw) as Record<string, unknown>)
      .filter((message) => message.type === "rpc")
    return sent.findLast((message) => message.operation === operation)
  }
}

function stubFetchTicket(ticket = "ticket-1") {
  return vi.fn(
    async () =>
      new Response(JSON.stringify({ ticket }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
  )
}

async function connectReadyClient() {
  const client = new SandboxSessionClient("sandbox-1")
  client.start()
  await vi.advanceTimersByTimeAsync(0)
  const socket = MockWebSocket.instances.at(-1)
  expect(socket).toBeDefined()
  socket!.receive({ type: "ready" })
  expect(client.getState()).toBe("ready")
  return { client, socket: socket! }
}

describe("SandboxSessionClient", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    MockWebSocket.instances = []
    vi.stubGlobal("window", { location: { origin: "http://localhost:3000" } })
    vi.stubGlobal("WebSocket", MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  describe("重连退避", () => {
    it("restart 和 stop 会中止尚未完成的会话票据请求", async () => {
      const signals: AbortSignal[] = []
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async (_input: RequestInfo | URL, init?: RequestInit) =>
            new Promise<Response>((_resolve, reject) => {
              const signal = init?.signal
              expect(signal).toBeInstanceOf(AbortSignal)
              signals.push(signal as AbortSignal)
              signal?.addEventListener("abort", () => reject(signal.reason), {
                once: true,
              })
            })
        )
      )
      const client = new SandboxSessionClient("sandbox-1")

      client.start()
      await vi.advanceTimersByTimeAsync(0)
      expect(signals).toHaveLength(1)

      client.restart()
      await vi.advanceTimersByTimeAsync(0)
      expect(signals[0].aborted).toBe(true)
      expect(signals).toHaveLength(2)

      client.stop()
      await vi.advanceTimersByTimeAsync(0)
      expect(signals[1].aborted).toBe(true)
      expect(client.getState()).toBe("disconnected")
    })

    it("忽略已被 restart 替换的旧 WebSocket 迟到消息", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket: first } = await connectReadyClient()

      client.restart()
      await vi.advanceTimersByTimeAsync(0)
      const second = MockWebSocket.instances.at(-1)
      expect(second).toBeDefined()
      expect(second).not.toBe(first)
      expect(client.getState()).toBe("connecting")

      first.receive({ type: "ready" })
      first.receive({ type: "closed", retryable: true })

      expect(client.getState()).toBe("connecting")
      expect(second!.closeCode).toBeUndefined()
      second!.receive({ type: "ready" })
      expect(client.getState()).toBe("ready")
      client.stop()
    })

    it("会话票据请求超时后进入可恢复的重试状态", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async (_input: RequestInfo | URL, init?: RequestInit) =>
            new Promise<Response>((_resolve, reject) => {
              const signal = init?.signal
              signal?.addEventListener("abort", () => reject(signal.reason), {
                once: true,
              })
            })
        )
      )
      const client = new SandboxSessionClient("sandbox-1")
      const states: Array<{ state: string; detail?: string }> = []
      client.onState((state, detail) => states.push({ state, detail }))

      client.start()
      await vi.advanceTimersByTimeAsync(sandboxSessionTicketTimeoutMs)

      expect(client.getState()).toBe("connecting")
      expect(states.at(-1)?.detail).toBe("创建沙箱会话超时，正在重试")
      client.stop()
    })

    it("断线后按 1s、2s 翻倍重连，ready 后重置为 1s", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      socket.close()
      expect(client.getState()).toBe("connecting")
      await vi.advanceTimersByTimeAsync(999)
      expect(MockWebSocket.instances).toHaveLength(1)
      await vi.advanceTimersByTimeAsync(1)
      expect(MockWebSocket.instances).toHaveLength(2)

      // 第二次断开（未 ready）：退避翻倍到 2s
      const second = MockWebSocket.instances[1]
      second.close()
      await vi.advanceTimersByTimeAsync(1999)
      expect(MockWebSocket.instances).toHaveLength(2)
      await vi.advanceTimersByTimeAsync(1)
      expect(MockWebSocket.instances).toHaveLength(3)

      // 连接稳定 10 秒后再次断开：退避重置为 1s
      const third = MockWebSocket.instances[2]
      third.receive({ type: "ready" })
      await vi.advanceTimersByTimeAsync(10_000)
      third.close()
      await vi.advanceTimersByTimeAsync(1000)
      expect(MockWebSocket.instances).toHaveLength(4)

      client.stop()
    })

    it("短暂 ready 后立即断开时保留退避，避免重连风暴", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      socket.close()
      await vi.advanceTimersByTimeAsync(1000)
      const second = MockWebSocket.instances[1]
      second.receive({ type: "ready" })
      second.receive({ type: "closed", retryable: true })

      await vi.advanceTimersByTimeAsync(1999)
      expect(MockWebSocket.instances).toHaveLength(2)
      await vi.advanceTimersByTimeAsync(1)
      expect(MockWebSocket.instances).toHaveLength(3)
      client.stop()
    })

    it("stop 后不再重连", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()
      socket.close()
      client.stop()
      await vi.advanceTimersByTimeAsync(30_000)
      expect(MockWebSocket.instances).toHaveLength(1)
      expect(client.getState()).toBe("disconnected")
    })

    it("会话服务暂时不可用时保持连接中并退避重试", async () => {
      const fetchTicket = vi.fn(
        async () =>
          new Response(JSON.stringify({ error: "沙箱会话服务尚未连接" }), {
            status: 503,
            headers: { "Content-Type": "application/json" },
          })
      )
      vi.stubGlobal("fetch", fetchTicket)
      const client = new SandboxSessionClient("sandbox-1")

      client.start()
      await vi.advanceTimersByTimeAsync(0)
      expect(client.getState()).toBe("connecting")
      expect(fetchTicket).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(999)
      expect(fetchTicket).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1)
      expect(fetchTicket).toHaveBeenCalledTimes(2)
      client.stop()
    })

    it("结构化授权错误保留服务端消息且不重试", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            new Response(
              JSON.stringify({
                error: {
                  code: "forbidden",
                  message: "没有权限创建沙箱会话",
                  retryable: false,
                },
              }),
              {
                status: 403,
                headers: { "Content-Type": "application/json" },
              }
            )
        )
      )
      const client = new SandboxSessionClient("sandbox-1")
      const states: Array<{ state: string; detail?: string }> = []
      client.onState((state, detail) => states.push({ state, detail }))

      client.start()
      await vi.advanceTimersByTimeAsync(30_000)

      expect(client.getState()).toBe("error")
      expect(states.at(-1)).toEqual({
        state: "error",
        detail: "没有权限创建沙箱会话",
      })
      expect(fetch).toHaveBeenCalledTimes(1)
      client.stop()
    })

    it("结构化不可重试错误进入终态且不再请求票据", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            new Response(
              JSON.stringify({
                error: {
                  code: "conflict",
                  message: "沙箱当前未运行",
                  retryable: false,
                },
              }),
              {
                status: 409,
                headers: { "Content-Type": "application/json" },
              }
            )
        )
      )
      const client = new SandboxSessionClient("sandbox-1")
      const states: Array<{ state: string; detail?: string }> = []
      client.onState((state, detail) => states.push({ state, detail }))

      client.start()
      await vi.advanceTimersByTimeAsync(30_000)

      expect(client.getState()).toBe("error")
      expect(states.at(-1)).toEqual({
        state: "error",
        detail: "沙箱当前未运行",
      })
      expect(fetch).toHaveBeenCalledTimes(1)
      client.stop()
    })

    it("远程 shell 异常退出时自动创建新会话", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      socket.receive({ type: "closed", retryable: true })
      expect(client.getState()).toBe("connecting")
      expect(socket.closeCode).toBe(4001)
      await vi.advanceTimersByTimeAsync(999)
      expect(MockWebSocket.instances).toHaveLength(1)
      await vi.advanceTimersByTimeAsync(1)

      expect(MockWebSocket.instances).toHaveLength(2)
      expect(client.getState()).toBe("connecting")
      client.stop()
    })

    it("BoxLite portal 瞬时错误按退避策略自动重连", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      socket.receive({
        type: "error",
        error:
          "portal error: gRPC/tonic status: Unavailable, Connection refused",
      })
      expect(client.getState()).toBe("connecting")
      await vi.advanceTimersByTimeAsync(1000)

      expect(MockWebSocket.instances).toHaveLength(2)
      client.stop()
    })

    it("远程 shell 正常退出时保留手动重连状态", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      socket.receive({ type: "closed" })
      await vi.advanceTimersByTimeAsync(30_000)

      expect(MockWebSocket.instances).toHaveLength(1)
      expect(client.getState()).toBe("disconnected")
      client.stop()
    })
  })

  describe("pending 请求", () => {
    it("30 秒无响应时以超时错误拒绝", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client } = await connectReadyClient()
      const pending = client.request("list", "/")
      const assertion = expect(pending).rejects.toThrow("沙箱文件操作超时")
      await vi.advanceTimersByTimeAsync(30_000)
      await assertion
      client.stop()
    })

    it("收到 rpc-result 后正常解析并清除定时器", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()
      const pending = client.request<string>("read", "/a.txt")
      await vi.advanceTimersByTimeAsync(0)
      const request = socket.lastRequest("read")
      expect(request).toBeDefined()
      socket.receive({
        type: "rpc-result",
        requestId: request!.requestId,
        ok: true,
        result: "hello",
      })
      await expect(pending).resolves.toBe("hello")
      // 超时定时器已清除：继续推进时间也不应再触发拒绝
      await vi.advanceTimersByTimeAsync(60_000)
      client.stop()
    })
  })

  describe("分块上传", () => {
    it("某一分块失败后发送 upload-cancel 并拒绝", async () => {
      vi.stubGlobal("fetch", stubFetchTicket())
      const { client, socket } = await connectReadyClient()

      // 200 KiB → 两个分块（192 KiB + 8 KiB）
      const file = new File([new Uint8Array(200 * 1024)], "big.bin")
      const upload = client.uploadFile(file, "/big.bin")
      const assertion = expect(upload).rejects.toThrow("写入失败")

      await vi.advanceTimersByTimeAsync(0)
      const start = socket.lastRequest("upload-start")
      expect(start).toBeDefined()
      socket.receive({
        type: "rpc-result",
        requestId: start!.requestId,
        ok: true,
      })

      await vi.advanceTimersByTimeAsync(0)
      const chunk = socket.lastRequest("upload-chunk")
      expect(chunk).toBeDefined()
      socket.receive({
        type: "rpc-result",
        requestId: chunk!.requestId,
        ok: false,
        error: "写入失败",
      })

      await vi.advanceTimersByTimeAsync(0)
      const cancel = socket.lastRequest("upload-cancel")
      expect(cancel).toBeDefined()
      expect(cancel!.uploadId).toBe(start!.uploadId)
      socket.receive({
        type: "rpc-result",
        requestId: cancel!.requestId,
        ok: true,
      })

      await assertion
      client.stop()
    })
  })
})
