import { networkInterfaces } from "node:os"

import { buildWorkerAddressCandidates } from "@/lib/worker-address"

export const dynamic = "force-dynamic"
export const runtime = "nodejs"

export async function GET(request: Request) {
  const candidates = buildWorkerAddressCandidates({
    gatewayOrigin: new URL(request.url).origin,
    configuredOrigin: process.env.AGENTBOX_PUBLIC_URL,
    interfaceAddresses: localIPv4Addresses(),
  })

  for (const candidate of candidates) {
    if (await isHealthyAgentBoxGateway(candidate.url)) {
      return Response.json(candidate, {
        headers: { "Cache-Control": "no-store" },
      })
    }
  }

  return Response.json(
    { error: "未检测到物理服务器可访问的 AgentBox 后端地址" },
    { status: 503, headers: { "Cache-Control": "no-store" } }
  )
}

function localIPv4Addresses() {
  const addresses = new Set<string>()
  for (const entries of Object.values(networkInterfaces())) {
    for (const entry of entries ?? []) {
      if (entry.family === "IPv4" && !entry.internal) {
        addresses.add(entry.address)
      }
    }
  }
  return [...addresses]
}

async function isHealthyAgentBoxGateway(origin: string) {
  try {
    const response = await fetch(new URL("/api/auth/status", `${origin}/`), {
      cache: "no-store",
      signal: AbortSignal.timeout(1_500),
    })
    if (!response.ok) return false
    const body = (await response.json()) as { needsSetup?: unknown }
    return typeof body.needsSetup === "boolean"
  } catch {
    return false
  }
}
