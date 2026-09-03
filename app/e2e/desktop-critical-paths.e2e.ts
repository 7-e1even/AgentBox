import { expect, test, type Page } from "@playwright/test"

const apiOrigin = "http://127.0.0.1:18091"

type SessionMockOptions = {
  initialContent?: string
  readDelays?: number[]
  rootListFailures?: number
  writeDelays?: number[]
}

type SessionMockState = {
  listRequests: number
  pendingWrites: number
  persistedContent: string
  socketCloses: number
  socketConnections: number
  socketReadyState: number
  writeCompletions: string[]
  writes: string[]
}

test.beforeEach(async ({ request }) => {
  const response = await request.post(`${apiOrigin}/__e2e/reset`)
  expect(response.ok()).toBe(true)
})

test("signs in with labeled keyboard controls", async ({ context, page }) => {
  await context.addCookies([
    {
      name: "e2e-anonymous",
      value: "1",
      url: "http://127.0.0.1:13100",
    },
  ])

  await page.goto("/login")

  await expect(page.getByText("登录 AgentBox", { exact: true })).toBeVisible()
  await page.getByLabel("用户名").fill("e2e-admin")
  await page.getByLabel("密码").fill("password123")
  await page.getByLabel("密码").press("Enter")

  await expect(page).toHaveURL(/\/overview$/)
  await expect(
    page.getByText("E2E Project", { exact: true }).first()
  ).toBeVisible()
})

test("switches the next model request without leaving the workspace", async ({
  page,
}) => {
  await installSessionMock(page)
  await page.goto("/sandboxes/sandbox-one")
  await expect(page.getByText("Desktop Sandbox", { exact: true })).toBeVisible()
  await expect
    .poll(async () => {
      const state = await readSessionMockDiagnostics(page)
      return {
        socketCloses: state.socketCloses,
        socketConnections: state.socketConnections,
        socketReadyState: state.socketReadyState,
      }
    })
    .toEqual({ socketCloses: 0, socketConnections: 1, socketReadyState: 1 })

  const originalURL = page.url()
  const originalSession = await readSessionMockDiagnostics(page)
  await page.getByRole("button", { name: /模型源/ }).click()
  await expect(page.getByRole("heading", { name: "切换模型源" })).toBeVisible()

  const modelInput = page.getByLabel("目标模型")
  await modelInput.fill("New Model")
  await page.getByRole("option", { name: /New Model/ }).click()
  await page.getByRole("button", { name: "确认切换" }).click()

  await expect(page.getByRole("heading", { name: "切换模型源" })).toBeHidden()
  await expect(
    page.getByRole("button", { name: /Next Service · New Model/ })
  ).toBeVisible()
  expect(page.url()).toBe(originalURL)
  await expect
    .poll(async () => {
      const state = await readSessionMockDiagnostics(page)
      return {
        socketCloses: state.socketCloses,
        socketConnections: state.socketConnections,
        socketReadyState: state.socketReadyState,
      }
    })
    .toEqual({
      socketCloses: originalSession.socketCloses,
      socketConnections: originalSession.socketConnections,
      socketReadyState: originalSession.socketReadyState,
    })
})

test("refreshes a stale model source and preserves the pending target", async ({
  page,
  request,
}) => {
  await installSessionMock(page)
  await page.goto("/sandboxes/sandbox-one")
  await page.getByRole("button", { name: /模型源/ }).click()

  const modelInput = page.getByLabel("目标模型")
  await modelInput.fill("New Model")
  await page.getByRole("option", { name: /New Model/ }).click()
  const configured = await request.post(`${apiOrigin}/__e2e/config`, {
    data: { modelSourceConflictOnce: true },
  })
  expect(configured.ok()).toBe(true)
  await expect(
    page.getByRole("note").filter({ hasText: "当前绑定" })
  ).toContainText("Other Service · Other Model", { timeout: 20_000 })

  await page.getByRole("button", { name: "确认切换" }).click()

  await expect(page.getByRole("heading", { name: "切换模型源" })).toBeVisible()
  await expect(page.getByText(/模型源已被其他操作更新/)).toBeVisible()
  await expect(
    page.getByRole("note").filter({ hasText: "当前绑定" })
  ).toContainText("Other Service · Other Model")
  await expect(modelInput).toHaveValue(/Next Service · New Model/)
  await expect(page.getByRole("button", { name: "确认切换" })).toBeEnabled()

  await page.getByRole("button", { name: "确认切换" }).click()
  await expect(page.getByRole("heading", { name: "切换模型源" })).toBeHidden()
  await expect(
    page.getByRole("button", { name: /Next Service · New Model/ })
  ).toBeVisible()

  const stateResponse = await request.get(`${apiOrigin}/__e2e/state`)
  expect(stateResponse.ok()).toBe(true)
  const state = (await stateResponse.json()) as {
    modelSourceRequests: Array<Record<string, string>>
  }
  expect(state.modelSourceRequests).toEqual([
    {
      credentialId: "source-next",
      expectedCredentialId: "slot-primary",
      expectedModelId: "model-old",
      modelId: "model-new",
      slotCredentialId: "slot-primary",
    },
    {
      credentialId: "source-next",
      expectedCredentialId: "source-other",
      expectedModelId: "model-other",
      modelId: "model-new",
      slotCredentialId: "slot-primary",
    },
  ])
})

test("retries a failed workspace directory only after user action", async ({
  page,
}) => {
  await installSessionMock(page, { rootListFailures: 1 })
  await page.goto("/sandboxes/sandbox-one")

  const directoryError = page.getByRole("alert").filter({
    hasText: "无法读取根目录",
  })
  await expect(directoryError).toContainText("E2E 根目录暂时不可用")
  await expect
    .poll(() => readSessionMockState(page))
    .toEqual({
      listRequests: 1,
      writes: [],
    })
  await page.waitForTimeout(1_200)
  await expect
    .poll(() => readSessionMockState(page))
    .toEqual({
      listRequests: 1,
      writes: [],
    })

  await directoryError.getByRole("button", { name: "重试" }).click()

  await expect(page.getByRole("button", { name: "README.md" })).toBeVisible()
  await expect
    .poll(() => readSessionMockState(page))
    .toEqual({
      listRequests: 2,
      writes: [],
    })
})

test("shows session-ticket failure details and reconnects on demand", async ({
  page,
  request,
}) => {
  const configured = await request.post(`${apiOrigin}/__e2e/config`, {
    data: { sessionTicketFailures: 1 },
  })
  expect(configured.ok()).toBe(true)
  await installSessionMock(page)
  await page.goto("/sandboxes/sandbox-one")

  const connectionError = page.getByRole("alert").filter({
    hasText: "实时会话正在重连",
  })
  await expect(connectionError).toContainText(
    "E2E 会话票据暂时不可用，正在重试"
  )
  await expect
    .poll(async () => {
      const response = await request.get(`${apiOrigin}/__e2e/state`)
      expect(response.ok()).toBe(true)
      return (await response.json()).sessionTicketRequests as number
    })
    .toBe(1)

  const reconnectRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/api/sandboxes/sandbox-one/session-ticket",
    { timeout: 500 }
  )
  await Promise.all([
    reconnectRequest,
    connectionError.getByRole("button", { name: "重新连接" }).click(),
  ])

  await expect
    .poll(async () => {
      const response = await request.get(`${apiOrigin}/__e2e/state`)
      expect(response.ok()).toBe(true)
      return (await response.json()).sessionTicketRequests as number
    })
    .toBe(2)
  await expect(page.getByText("已连接", { exact: true }).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "README.md" })).toBeVisible()
})

test("persists a reverted latest edit during an in-flight save", async ({
  page,
}) => {
  const initialContent = "# E2E workspace\n"
  await installSessionMock(page, {
    initialContent,
    writeDelays: [2_000, 0],
  })
  await page.goto("/sandboxes/sandbox-one")
  await page.getByRole("button", { name: "README.md" }).click()

  const editor = page.getByLabel("编辑 /README.md")
  await editor.fill("first saved version")
  await editor.press("Control+s")
  await expect
    .poll(async () => (await readSessionMockState(page)).writes)
    .toEqual(["first saved version"])

  await editor.fill(initialContent)

  const dismissedDialog = page.waitForEvent("dialog")
  const blockedNavigation = page
    .getByRole("button", { name: "打开设置" })
    .click()
  const dialog = await dismissedDialog
  expect(dialog.message()).toBe("存在未保存的文件，确定要离开工作区吗？")
  await dialog.dismiss()
  await blockedNavigation
  await expect(page).toHaveURL(/\/sandboxes\/sandbox-one$/)

  await editor.press("Control+s")

  await expect
    .poll(async () => {
      const state = await readSessionMockDiagnostics(page)
      return {
        pendingWrites: state.pendingWrites,
        persistedContent: state.persistedContent,
        writeCompletions: state.writeCompletions,
        writes: state.writes,
      }
    })
    .toEqual({
      pendingWrites: 0,
      persistedContent: initialContent,
      writeCompletions: ["first saved version", initialContent],
      writes: ["first saved version", initialContent],
    })
  await expect(page.getByLabel("未保存")).toHaveCount(0)
})

test("ignores a stale file read after the workspace generation ends", async ({
  page,
}) => {
  await installSessionMock(page, { readDelays: [1_000] })
  await page.goto("/sandboxes/sandbox-one")
  await page.getByRole("button", { name: "README.md" }).click()

  await page.getByRole("button", { name: "打开设置" }).click()

  await expect(page).toHaveURL(/\/settings$/)
  await page.waitForTimeout(1_100)
  await expect(page.getByText("无法打开文件", { exact: true })).toHaveCount(0)
})

test("asks before topbar navigation would discard dirty workspace edits", async ({
  page,
}) => {
  await installSessionMock(page)
  await page.goto("/sandboxes/sandbox-one")
  await page.getByRole("button", { name: "README.md" }).click()
  await page.getByLabel("编辑 /README.md").fill("unsaved navigation edit")

  const settingsButton = page.getByRole("button", { name: "打开设置" })
  const dismissedDialog = page.waitForEvent("dialog")
  const blockedNavigation = settingsButton.click()
  const firstDialog = await dismissedDialog
  expect(firstDialog.message()).toBe("存在未保存的文件，确定要离开工作区吗？")
  await firstDialog.dismiss()
  await blockedNavigation
  await expect(page).toHaveURL(/\/sandboxes\/sandbox-one$/)

  const acceptedDialog = page.waitForEvent("dialog")
  const allowedNavigation = settingsButton.click()
  const secondDialog = await acceptedDialog
  expect(secondDialog.message()).toBe("存在未保存的文件，确定要离开工作区吗？")
  await secondDialog.accept()
  await allowedNavigation
  await expect(page).toHaveURL(/\/settings$/)
})

test("keeps dirty workspace state when Back is dismissed", async ({ page }) => {
  await installSessionMock(page)
  await page.goto("/sandboxes")
  await page.getByRole("link", { name: "打开 Desktop Sandbox 工作台" }).click()
  await expect(page).toHaveURL(/\/sandboxes\/sandbox-one$/)
  await page.getByRole("button", { name: "README.md" }).click()

  const editor = page.getByLabel("编辑 /README.md")
  const dirtyContent = "unsaved browser history edit"
  await editor.fill(dirtyContent)

  const dismissedDialog = page.waitForEvent("dialog")
  await page.evaluate(() => window.history.back())
  const firstDialog = await dismissedDialog
  expect(firstDialog.message()).toBe("存在未保存的文件，确定要离开工作区吗？")
  await firstDialog.dismiss()

  await expect(page).toHaveURL(/\/sandboxes\/sandbox-one$/)
  await expect(editor).toHaveValue(dirtyContent)

  const acceptedDialog = page.waitForEvent("dialog")
  await page.evaluate(() => window.history.back())
  const secondDialog = await acceptedDialog
  expect(secondDialog.message()).toBe("存在未保存的文件，确定要离开工作区吗？")
  await secondDialog.accept()

  await expect(page).toHaveURL(/\/sandboxes$/)
})

async function installSessionMock(
  page: Page,
  options: SessionMockOptions = {}
) {
  await page.addInitScript(
    (configuration: Required<SessionMockOptions>) => {
      const state: SessionMockState = {
        listRequests: 0,
        pendingWrites: 0,
        persistedContent: configuration.initialContent,
        socketCloses: 0,
        socketConnections: 0,
        socketReadyState: 3,
        writeCompletions: [],
        writes: [],
      }
      let remainingRootListFailures = configuration.rootListFailures
      let readIndex = 0
      let writeIndex = 0
      const NativeWebSocket = window.WebSocket
      ;(
        window as Window & { __AGENTBOX_E2E_SESSION__?: SessionMockState }
      ).__AGENTBOX_E2E_SESSION__ = state

      class E2EWebSocket extends EventTarget {
        static readonly CONNECTING = 0
        static readonly OPEN = 1
        static readonly CLOSING = 2
        static readonly CLOSED = 3

        readonly url: string
        readyState = E2EWebSocket.CONNECTING

        constructor(url: string | URL) {
          super()
          this.url = String(url)
          state.socketConnections += 1
          state.socketReadyState = E2EWebSocket.CONNECTING
          queueMicrotask(() => {
            if (this.readyState !== E2EWebSocket.CONNECTING) return
            this.readyState = E2EWebSocket.OPEN
            state.socketReadyState = E2EWebSocket.OPEN
            this.dispatchEvent(new Event("open"))
            this.respond({ type: "ready" })
          })
        }

        send(data: string | ArrayBufferLike | Blob | ArrayBufferView) {
          if (
            this.readyState !== E2EWebSocket.OPEN ||
            typeof data !== "string"
          ) {
            return
          }
          const message = JSON.parse(data) as {
            type?: string
            requestId?: string
            operation?: string
            path?: string
            content?: string
          }
          if (message.type !== "rpc" || !message.requestId) return

          if (message.operation === "list") {
            state.listRequests += 1
            if (message.path === "/" && remainingRootListFailures > 0) {
              remainingRootListFailures -= 1
              this.respond({
                type: "rpc-result",
                requestId: message.requestId,
                ok: false,
                error: "E2E 根目录暂时不可用",
              })
              return
            }
            this.respond({
              type: "rpc-result",
              requestId: message.requestId,
              ok: true,
              result:
                message.path === "/"
                  ? [
                      {
                        name: "README.md",
                        path: "/README.md",
                        type: "file",
                        size: configuration.initialContent.length,
                        modifiedAt: 1_788_393_600,
                      },
                    ]
                  : [],
            })
            return
          }
          if (message.operation === "read") {
            const delay = configuration.readDelays[readIndex] ?? 0
            readIndex += 1
            this.respond(
              {
                type: "rpc-result",
                requestId: message.requestId,
                ok: true,
                result: configuration.initialContent,
              },
              delay
            )
            return
          }
          if (message.operation === "write") {
            const content = message.content ?? ""
            state.writes.push(content)
            state.pendingWrites += 1
            const delay = configuration.writeDelays[writeIndex] ?? 0
            writeIndex += 1
            this.respond(
              {
                type: "rpc-result",
                requestId: message.requestId,
                ok: true,
                result: message.path,
              },
              delay,
              () => {
                state.pendingWrites -= 1
                state.persistedContent = content
                state.writeCompletions.push(content)
              }
            )
            return
          }
          this.respond({
            type: "rpc-result",
            requestId: message.requestId,
            ok: false,
            error: `E2E 不支持文件操作：${message.operation ?? "unknown"}`,
          })
        }

        close(code = 1000, reason = "") {
          if (this.readyState === E2EWebSocket.CLOSED) return
          this.readyState = E2EWebSocket.CLOSED
          state.socketCloses += 1
          state.socketReadyState = E2EWebSocket.CLOSED
          queueMicrotask(() => {
            this.dispatchEvent(
              new CloseEvent("close", {
                code,
                reason,
                wasClean: code === 1000,
              })
            )
          })
        }

        private respond(
          message: object,
          delay = 0,
          beforeDispatch?: () => void
        ) {
          window.setTimeout(() => {
            if (this.readyState !== E2EWebSocket.OPEN) return
            beforeDispatch?.()
            this.dispatchEvent(
              new MessageEvent("message", { data: JSON.stringify(message) })
            )
          }, delay)
        }
      }

      const SessionWebSocket = new Proxy(NativeWebSocket, {
        construct(target, argumentsList) {
          const url = String(argumentsList[0] ?? "")
          const path = new URL(url, window.location.href).pathname
          if (path !== "/api/sandboxes/sandbox-one/session") {
            return Reflect.construct(target, argumentsList)
          }
          return new E2EWebSocket(url)
        },
      })

      Object.defineProperty(window, "WebSocket", {
        configurable: true,
        value: SessionWebSocket,
        writable: true,
      })
    },
    {
      initialContent: options.initialContent ?? "# E2E workspace\n",
      readDelays: options.readDelays ?? [],
      rootListFailures: options.rootListFailures ?? 0,
      writeDelays: options.writeDelays ?? [],
    }
  )
}

async function readSessionMockState(page: Page) {
  return page.evaluate(() => {
    const state = (
      window as Window & { __AGENTBOX_E2E_SESSION__?: SessionMockState }
    ).__AGENTBOX_E2E_SESSION__
    if (!state) throw new Error("E2E session mock was not installed")
    return { listRequests: state.listRequests, writes: [...state.writes] }
  })
}

async function readSessionMockDiagnostics(page: Page) {
  return page.evaluate(() => {
    const state = (
      window as Window & { __AGENTBOX_E2E_SESSION__?: SessionMockState }
    ).__AGENTBOX_E2E_SESSION__
    if (!state) throw new Error("E2E session mock was not installed")
    return {
      pendingWrites: state.pendingWrites,
      persistedContent: state.persistedContent,
      socketCloses: state.socketCloses,
      socketConnections: state.socketConnections,
      socketReadyState: state.socketReadyState,
      writeCompletions: [...state.writeCompletions],
      writes: [...state.writes],
    }
  })
}
