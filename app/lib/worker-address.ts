export type WorkerAddressSource =
  | "configured"
  | "gateway"
  | "network-interface"

export type WorkerAddressCandidate = {
  url: string
  source: WorkerAddressSource
}

export function buildWorkerAddressCandidates({
  gatewayOrigin,
  configuredOrigin,
  interfaceAddresses,
}: {
  gatewayOrigin: string
  configuredOrigin?: string
  interfaceAddresses: string[]
}): WorkerAddressCandidate[] {
  const candidates: WorkerAddressCandidate[] = []
  const seen = new Set<string>()
  const add = (value: string | undefined, source: WorkerAddressSource) => {
    const url = normalizeHttpOrigin(value)
    if (!url || seen.has(url)) return
    seen.add(url)
    candidates.push({ url, source })
  }

  add(configuredOrigin, "configured")

  const parsedGateway = parseHttpURL(gatewayOrigin)
  if (!parsedGateway) return candidates

  if (!isLoopbackHostname(parsedGateway.hostname)) {
    add(parsedGateway.origin, "gateway")
  }

  for (const address of interfaceAddresses) {
    if (!isLoopbackHostname(address)) {
      add(
        originForHost(
          parsedGateway.protocol,
          address,
          parsedGateway.port
        ),
        "network-interface"
      )
    }
  }

  return candidates
}

export function normalizeHttpOrigin(value: string | undefined) {
  const parsed = parseHttpURL(value)
  if (parsed?.protocol === "http:" && !isLoopbackHostname(parsed.hostname)) {
    return null
  }
  return parsed?.origin ?? null
}

function parseHttpURL(value: string | undefined) {
  if (!value?.trim()) return null
  try {
    const parsed = new URL(value.trim())
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return null
    }
    return parsed
  } catch {
    return null
  }
}

function isLoopbackHostname(hostname: string) {
  const normalized = hostname.replace(/^\[|\]$/g, "").toLowerCase()
  if (normalized === "localhost" || normalized === "::1") return true
  const octets = normalized.split(".")
  return (
    octets.length === 4 &&
    octets[0] === "127" &&
    octets.every((octet) => /^\d{1,3}$/.test(octet) && Number(octet) <= 255)
  )
}

function originForHost(protocol: string, hostname: string, port: string) {
  const formattedHostname = hostname.includes(":") ? `[${hostname}]` : hostname
  return `${protocol}//${formattedHostname}${port ? `:${port}` : ""}`
}
