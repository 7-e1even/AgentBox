import { createServer } from "node:http"

const port = Number(process.argv[2] || 18091)
const now = "2026-09-03T00:00:00Z"
const admin = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "E2E 管理员",
  username: "e2e-admin",
  email: "e2e@example.com",
  role: "admin",
  status: "active",
  preferences: {
    successNotifications: true,
    density: "comfortable",
    showCapabilities: true,
    showInfrastructure: true,
    showGovernance: true,
  },
  lastLoginAt: now,
  createdAt: now,
  updatedAt: now,
}
const project = resource("project-one", "project", null, "E2E Project", {
  emoji: "🧪",
})
const runtime = resource(
  "runtime-one",
  "runtime",
  project.id,
  "Desktop Runtime",
  {
    credentialIds: ["slot-primary"],
    modelBindings: { "slot-primary": "model-old" },
  }
)
let sandbox = sandboxFixture()
let e2eState = e2eStateFixture()
const credentials = [
  credential("slot-primary", "Primary Service", "model-old", "Old Model"),
  credential("source-next", "Next Service", "model-new", "New Model"),
  credential("source-other", "Other Service", "model-other", "Other Model"),
]

const server = createServer(async (request, response) => {
  const url = new URL(request.url || "/", `http://${request.headers.host}`)
  if (request.method === "POST" && url.pathname === "/__e2e/reset") {
    sandbox = sandboxFixture()
    e2eState = e2eStateFixture()
    return json(response, 204)
  }
  if (request.method === "POST" && url.pathname === "/__e2e/config") {
    const input = await readJSON(request)
    if ("sessionTicketFailures" in input) {
      if (
        typeof input.sessionTicketFailures !== "number" ||
        !Number.isInteger(input.sessionTicketFailures) ||
        input.sessionTicketFailures < 0
      ) {
        return json(response, 400, { error: "invalid sessionTicketFailures" })
      }
      e2eState.sessionTicketFailures = input.sessionTicketFailures
    }
    if ("modelSourceConflictOnce" in input) {
      if (typeof input.modelSourceConflictOnce !== "boolean") {
        return json(response, 400, { error: "invalid modelSourceConflictOnce" })
      }
      e2eState.modelSourceConflictOnce = input.modelSourceConflictOnce
      if (input.modelSourceConflictOnce) {
        setSandboxModelSource("slot-primary", "source-other", "model-other")
      }
    }
    return json(response, 200, e2eState)
  }
  if (request.method === "GET" && url.pathname === "/__e2e/state") {
    return json(response, 200, e2eState)
  }
  if (request.method === "GET" && url.pathname === "/api/auth/status") {
    return json(response, 200, {
      needsSetup: false,
      setupCodeRequired: false,
    })
  }
  if (request.method === "POST" && url.pathname === "/api/auth/login") {
    const input = await readJSON(request)
    if (input.username !== "e2e-admin" || input.password !== "password123") {
      return json(response, 401, { error: "用户名或密码错误" })
    }
    return json(
      response,
      200,
      { user: admin },
      {
        "Set-Cookie":
          "e2e-anonymous=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax",
      }
    )
  }
  if (request.method === "GET" && url.pathname === "/api/auth/me") {
    if ((request.headers.cookie || "").includes("e2e-anonymous=1")) {
      return json(response, 401, { error: "unauthorized" })
    }
    return json(response, 200, { user: admin })
  }
  if (request.method === "GET" && url.pathname === "/api/resources") {
    const kind = url.searchParams.get("kind")
    const resources =
      kind === "project"
        ? [project]
        : kind === "image"
          ? []
          : [project, runtime, sandbox]
    return json(response, 200, { resources })
  }
  if (
    request.method === "GET" &&
    url.pathname === `/api/resources/${sandbox.id}`
  ) {
    return json(response, 200, { resource: sandbox })
  }
  if (
    request.method === "GET" &&
    url.pathname === `/api/resources/${runtime.id}`
  ) {
    return json(response, 200, { resource: runtime })
  }
  if (request.method === "GET" && url.pathname === "/api/servers") {
    return json(response, 200, { servers: [], workerVersion: "e2e" })
  }
  if (request.method === "GET" && url.pathname === "/api/credentials") {
    return json(response, 200, { credentials })
  }
  if (request.method === "GET" && url.pathname === "/api/network-proxies") {
    return json(response, 200, { proxies: [] })
  }
  if (
    request.method === "PATCH" &&
    url.pathname === `/api/sandboxes/${sandbox.id}/model-source`
  ) {
    const input = await readJSON(request)
    e2eState.modelSourceRequests.push(input)
    if (e2eState.modelSourceConflictOnce) {
      e2eState.modelSourceConflictOnce = false
    }
    const current = sandbox.spec.runtimeModelSources[input.slotCredentialId]
    if (
      !current ||
      input.expectedCredentialId !== current.credentialId ||
      input.expectedModelId !== current.modelId
    ) {
      return json(response, 409, {
        error: {
          code: "conflict",
          message: "模型源已被其他操作更新",
          retryable: false,
        },
      })
    }
    setSandboxModelSource(
      input.slotCredentialId,
      input.credentialId,
      input.modelId
    )
    return json(response, 200, { resource: sandbox })
  }
  if (
    request.method === "POST" &&
    url.pathname === `/api/sandboxes/${sandbox.id}/session-ticket`
  ) {
    e2eState.sessionTicketRequests += 1
    if (e2eState.sessionTicketFailures > 0) {
      e2eState.sessionTicketFailures -= 1
      return json(response, 503, {
        error: {
          code: "worker_session_unavailable",
          message: "E2E 会话票据暂时不可用",
          retryable: true,
        },
      })
    }
    return json(response, 200, {
      ticket: `e2e-ticket-${e2eState.sessionTicketRequests}`,
      expiresAt: "2026-09-03T00:05:00Z",
    })
  }
  return json(response, 404, {
    error: `unhandled E2E route: ${request.method} ${url.pathname}`,
  })
})

server.listen(port, "127.0.0.1")

function sandboxFixture() {
  return resource("sandbox-one", "sandbox", project.id, "Desktop Sandbox", {
    runtimeId: runtime.id,
    serverId: "00000000-0000-4000-8000-000000000002",
    status: "running",
    credentialIds: ["slot-primary"],
    modelBindings: { "slot-primary": "model-old" },
    runtimeModelSourcesComplete: true,
    runtimeModelSources: {
      "slot-primary": {
        credentialId: "slot-primary",
        modelId: "model-old",
        updatedAt: now,
      },
    },
  })
}

function e2eStateFixture() {
  return {
    modelSourceConflictOnce: false,
    modelSourceRequests: [],
    sessionTicketFailures: 0,
    sessionTicketRequests: 0,
  }
}

function setSandboxModelSource(slotCredentialId, credentialId, modelId) {
  sandbox = {
    ...sandbox,
    updatedAt: now,
    spec: {
      ...sandbox.spec,
      runtimeModelSources: {
        ...sandbox.spec.runtimeModelSources,
        [slotCredentialId]: { credentialId, modelId, updatedAt: now },
      },
    },
  }
}

function resource(id, kind, projectId, name, spec) {
  return {
    id,
    kind,
    projectId,
    name,
    description: "E2E fixture",
    enabled: true,
    specVersion: 1,
    generation: 1,
    observedGeneration: 1,
    spec,
    createdAt: now,
    updatedAt: now,
  }
}

function credential(id, name, modelId, modelName) {
  return {
    id,
    name,
    providerId: "openai",
    protocol: "openai-responses",
    endpoint: "https://example.invalid/v1",
    modelId,
    models: [{ id: modelId, name: modelName, group: "E2E", source: "manual" }],
    maskedSecret: "sk-••••e2e",
    enabled: true,
    lastCheckAt: now,
    lastCheckOk: true,
    lastCheckError: "",
    createdAt: now,
    updatedAt: now,
  }
}

async function readJSON(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  return JSON.parse(Buffer.concat(chunks).toString("utf8"))
}

function json(response, status, body, headers = {}) {
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
    ...headers,
  })
  response.end(body === undefined ? undefined : JSON.stringify(body))
}
