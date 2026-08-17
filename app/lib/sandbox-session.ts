export type SandboxSessionState =
  "disconnected" | "connecting" | "ready" | "error"

type SessionMessage = {
  type: string
  data?: string
  error?: string
  requestId?: string
  result?: unknown
  ok?: boolean
}

type PendingRequest = {
  resolve: (value: unknown) => void
  reject: (reason: Error) => void
  timeout: ReturnType<typeof setTimeout>
}

type StateListener = (state: SandboxSessionState, detail?: string) => void
type OutputListener = (data: string) => void

type FileOperation =
  | "list"
  | "read"
  | "write"
  | "upload-start"
  | "upload-chunk"
  | "upload-finish"
  | "upload-cancel"

const uploadChunkSize = 192 * 1024

export const sandboxUploadMaxSize = 50 * 1024 * 1024
export const sandboxFileReadMaxSize = 5 * 1024 * 1024

export class SandboxSessionClient {
  private socket: WebSocket | null = null
  private state: SandboxSessionState = "disconnected"
  private stopped = true
  private connectionGeneration = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectDelay = 1000
  private readonly stateListeners = new Set<StateListener>()
  private readonly outputListeners = new Set<OutputListener>()
  private readonly pending = new Map<string, PendingRequest>()
  private readyWaiters: Array<{
    resolve: () => void
    reject: (reason: Error) => void
  }> = []

  constructor(private readonly sandboxId: string) {}

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.connectionGeneration += 1
    void this.connect()
  }

  stop() {
    this.stopped = true
    this.connectionGeneration += 1
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = null
    this.socket?.close(1000, "workspace closed")
    this.socket = null
    this.failPending(new Error("沙箱会话已关闭"))
    this.setState("disconnected")
  }

  restart() {
    if (this.stopped) {
      this.start()
      return
    }
    this.connectionGeneration += 1
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = null
    const socket = this.socket
    this.socket = null
    socket?.close(1000, "reconnect")
    this.failPending(new Error("沙箱会话正在重新连接"))
    this.reconnectDelay = 1000
    void this.connect()
  }

  getState() {
    return this.state
  }

  onState(listener: StateListener) {
    this.stateListeners.add(listener)
    listener(this.state)
    return () => this.stateListeners.delete(listener)
  }

  onOutput(listener: OutputListener) {
    this.outputListeners.add(listener)
    return () => this.outputListeners.delete(listener)
  }

  sendInput(data: string) {
    if (this.state === "ready") this.send({ type: "input", data })
  }

  resize(cols: number, rows: number) {
    if (this.state === "ready") this.send({ type: "resize", cols, rows })
  }

  async request<T>(
    operation: FileOperation,
    path: string,
    content?: string,
    uploadId?: string
  ): Promise<T> {
    await this.waitUntilReady()
    const requestId = crypto.randomUUID()
    return new Promise<T>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(requestId)
        reject(new Error("沙箱文件操作超时"))
      }, 30_000)
      this.pending.set(requestId, {
        resolve: (value) => resolve(value as T),
        reject,
        timeout,
      })
      try {
        this.send({
          type: "rpc",
          requestId,
          operation,
          path,
          content,
          uploadId,
        })
      } catch (error) {
        clearTimeout(timeout)
        this.pending.delete(requestId)
        reject(error instanceof Error ? error : new Error("无法发送文件操作"))
      }
    })
  }

  async uploadFile(
    file: File,
    path: string,
    onProgress?: (percent: number) => void
  ) {
    if (file.size > sandboxUploadMaxSize) {
      throw new Error("单个文件不能超过 50 MiB")
    }

    const uploadId = crypto.randomUUID()
    await this.request<string>("upload-start", path, undefined, uploadId)
    try {
      if (file.size === 0) onProgress?.(100)
      for (let offset = 0; offset < file.size; offset += uploadChunkSize) {
        const bytes = new Uint8Array(
          await file.slice(offset, offset + uploadChunkSize).arrayBuffer()
        )
        await this.request<string>(
          "upload-chunk",
          path,
          encodeBase64(bytes),
          uploadId
        )
        onProgress?.(
          Math.round(
            (Math.min(offset + bytes.length, file.size) / file.size) * 100
          )
        )
      }
      await this.request<string>("upload-finish", path, undefined, uploadId)
    } catch (error) {
      await this.request<string>(
        "upload-cancel",
        path,
        undefined,
        uploadId
      ).catch(() => undefined)
      throw error
    }
  }

  private async connect() {
    if (this.stopped || this.socket) return
    const generation = this.connectionGeneration
    this.setState("connecting")
    try {
      const response = await fetch(
        `/api/sandboxes/${encodeURIComponent(this.sandboxId)}/session-ticket`,
        { method: "POST", credentials: "include" }
      )
      const payload = (await response.json().catch(() => null)) as {
        ticket?: string
        error?: string
      } | null
      if (!response.ok || !payload?.ticket) {
        const detail =
          payload?.error || `无法创建沙箱会话（HTTP ${response.status}）`
        if (response.status === 401 || response.status === 403) {
          this.setState("error", detail)
          return
        }
        throw new Error(detail)
      }
      if (this.stopped || generation !== this.connectionGeneration) return

      const socket = new WebSocket(sessionURL(this.sandboxId, payload.ticket))
      this.socket = socket
      socket.addEventListener("message", (event) =>
        this.handleMessage(event.data)
      )
      socket.addEventListener("error", () => {
        if (this.socket === socket) this.setState("error", "实时会话连接失败")
      })
      socket.addEventListener("close", () => {
        if (this.socket !== socket) return
        this.socket = null
        this.failPending(new Error("Worker 会话已断开"))
        if (this.stopped) {
          this.setState("disconnected")
          return
        }
        this.setState("connecting", "Worker 会话已断开，正在重连")
        this.scheduleReconnect()
      })
    } catch (error) {
      this.socket = null
      const detail = error instanceof Error ? error.message : "实时会话连接失败"
      this.setState("error", detail)
      if (!this.stopped) this.scheduleReconnect()
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer || this.stopped) return
    const delay = this.reconnectDelay
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, 10_000)
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      void this.connect()
    }, delay)
  }

  private handleMessage(raw: unknown) {
    if (typeof raw !== "string") return
    let message: SessionMessage
    try {
      message = JSON.parse(raw) as SessionMessage
    } catch {
      return
    }
    if (message.type === "ready") {
      this.reconnectDelay = 1000
      this.setState("ready")
      const waiters = this.readyWaiters
      this.readyWaiters = []
      for (const waiter of waiters) waiter.resolve()
      return
    }
    if (message.type === "output" && message.data) {
      for (const listener of this.outputListeners) listener(message.data)
      return
    }
    if (message.type === "error") {
      const detail = message.error || "沙箱会话发生错误"
      this.setState("error", detail)
      for (const listener of this.outputListeners) {
        listener(`\r\n\x1b[31m${detail}\x1b[0m\r\n`)
      }
      return
    }
    if (message.type === "closed") {
      this.setState("disconnected", "远程 shell 已退出")
      return
    }
    if (message.type === "rpc-result" && message.requestId) {
      const pending = this.pending.get(message.requestId)
      if (!pending) return
      clearTimeout(pending.timeout)
      this.pending.delete(message.requestId)
      if (message.ok) pending.resolve(message.result)
      else pending.reject(new Error(message.error || "文件操作失败"))
    }
  }

  private waitUntilReady() {
    if (this.state === "ready") return Promise.resolve()
    if (this.stopped) return Promise.reject(new Error("沙箱会话尚未启动"))
    return new Promise<void>((resolve, reject) => {
      let wrappedResolve = () => undefined
      const timeout = setTimeout(() => {
        this.readyWaiters = this.readyWaiters.filter(
          (waiter) => waiter.resolve !== wrappedResolve
        )
        reject(new Error("等待沙箱实时会话超时"))
      }, 30_000)
      wrappedResolve = () => {
        clearTimeout(timeout)
        resolve()
      }
      const wrappedReject = (reason: Error) => {
        clearTimeout(timeout)
        reject(reason)
      }
      this.readyWaiters.push({ resolve: wrappedResolve, reject: wrappedReject })
    })
  }

  private send(value: object) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      throw new Error("沙箱实时会话未连接")
    }
    this.socket.send(JSON.stringify(value))
  }

  private setState(state: SandboxSessionState, detail?: string) {
    this.state = state
    for (const listener of this.stateListeners) listener(state, detail)
  }

  private failPending(error: Error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout)
      pending.reject(error)
    }
    this.pending.clear()
    for (const waiter of this.readyWaiters) waiter.reject(error)
    this.readyWaiters = []
  }
}

function encodeBase64(bytes: Uint8Array) {
  let binary = ""
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(
      ...bytes.subarray(offset, Math.min(offset + 0x8000, bytes.length))
    )
  }
  return btoa(binary)
}

function sessionURL(sandboxId: string, ticket: string) {
  const url = new URL(
    `/api/sandboxes/${encodeURIComponent(sandboxId)}/session`,
    window.location.origin
  )
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:"
  url.searchParams.set("ticket", ticket)
  return url.toString()
}
