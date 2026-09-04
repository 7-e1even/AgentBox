import { supportedAgentToolList, type AgentToolId } from "./agent-tools"

export type MCPHeaderReference = {
  name: string
  valueFrom: string
}

type ResourceLike = {
  id: string
  kind: string
  enabled?: boolean
  spec: Record<string, unknown>
}

export const mcpSupportedAgentToolIds = [
  "claude-code",
  "codex",
  "deepseek-harness",
  "gemini-cli",
  "opencode",
] as const satisfies readonly AgentToolId[]

const mcpSupportedAgentTools = new Set<string>(mcpSupportedAgentToolIds)
const referencePattern = /^(?:env|secret):\/\/[A-Za-z_][A-Za-z0-9_]*$/

export function mcpAgentToolCompatibility(value: unknown) {
  const tools = supportedAgentToolList(value)
  return {
    supported: tools.filter((tool) => mcpSupportedAgentTools.has(tool)),
    unsupported: tools.filter((tool) => !mcpSupportedAgentTools.has(tool)),
  }
}

export function hasOnlyUnsupportedMCPAgentTools(
  agentTools: unknown,
  mcpServerIds: unknown
) {
  if (!Array.isArray(mcpServerIds) || mcpServerIds.length === 0) return false
  const compatibility = mcpAgentToolCompatibility(agentTools)
  return compatibility.supported.length === 0
}

export function isMCPValueReference(value: string) {
  return referencePattern.test(value.trim())
}

export function mcpValueFromVariable(spec: Record<string, unknown>) {
  const key = typeof spec.key === "string" ? spec.key.trim() : ""
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) return ""
  if (spec.mode === "value-ref") return `env://${key}`
  if (spec.mode === "secret-ref") return `secret://${key}`

  // Old Variable records can omit mode. Infer only from the source scheme;
  // never copy the Worker-side source name into an MCP client config.
  const reference =
    typeof spec.reference === "string" ? spec.reference.trim() : ""
  if (reference.startsWith("env://")) return `env://${key}`
  if (reference.startsWith("secret://")) return `secret://${key}`
  return ""
}

export function requiredMCPVariableIds(
  mcpServerIds: unknown,
  resources: readonly ResourceLike[]
) {
  const selected = new Set(
    Array.isArray(mcpServerIds)
      ? mcpServerIds.filter((item): item is string => typeof item === "string")
      : []
  )
  const variablesByValueFrom = new Map<string, string>()
  for (const resource of resources) {
    if (resource.kind !== "variable" || resource.enabled === false) continue
    const valueFrom = mcpValueFromVariable(resource.spec)
    if (valueFrom && !variablesByValueFrom.has(valueFrom)) {
      variablesByValueFrom.set(valueFrom, resource.id)
    }
  }

  const result = new Set<string>()
  for (const resource of resources) {
    if (resource.kind !== "mcp" || !selected.has(resource.id)) continue
    for (const header of normalizeMCPHeaders(resource.spec.headers)) {
      const variableId = variablesByValueFrom.get(header.valueFrom)
      if (variableId) result.add(variableId)
    }
  }
  return [...result]
}

export function mergeRequiredMCPVariableIds(
  mcpServerIds: unknown,
  variableIds: unknown,
  resources: readonly ResourceLike[]
) {
  return [
    ...new Set([
      ...(Array.isArray(variableIds)
        ? variableIds.filter((item): item is string => typeof item === "string")
        : []),
      ...requiredMCPVariableIds(mcpServerIds, resources),
    ]),
  ]
}

export function unresolvedMCPHeaderReferences(
  mcpServerIds: unknown,
  resources: readonly ResourceLike[]
) {
  const selected = new Set(
    Array.isArray(mcpServerIds)
      ? mcpServerIds.filter((item): item is string => typeof item === "string")
      : []
  )
  const headers: MCPHeaderReference[] = []
  for (const resource of resources) {
    if (resource.kind !== "mcp" || !selected.has(resource.id)) continue
    headers.push(...normalizeMCPHeaders(resource.spec.headers))
  }
  return unresolvedMCPValueReferences(headers, resources)
}

export function unresolvedMCPValueReferences(
  headers: unknown,
  resources: readonly ResourceLike[]
) {
  const available = new Set(
    resources
      .filter(
        (resource) => resource.kind === "variable" && resource.enabled !== false
      )
      .map((resource) => mcpValueFromVariable(resource.spec))
      .filter(Boolean)
  )
  return [
    ...new Set(
      normalizeMCPHeaders(headers)
        .map((header) => header.valueFrom)
        .filter((valueFrom) => valueFrom && !available.has(valueFrom))
    ),
  ]
}

export function missingMCPVariableIds(
  mcpServerIds: unknown,
  variableIds: unknown,
  resources: readonly ResourceLike[]
) {
  const selected = new Set(
    Array.isArray(variableIds)
      ? variableIds.filter((item): item is string => typeof item === "string")
      : []
  )
  return requiredMCPVariableIds(mcpServerIds, resources).filter(
    (id) => !selected.has(id)
  )
}

export function normalizeMCPArgs(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.filter((item): item is string => typeof item === "string")
  }
  return typeof value === "string" ? splitLegacyArgs(value) : []
}

export function normalizeMCPHeaders(value: unknown): MCPHeaderReference[] {
  if (Array.isArray(value)) {
    return value
      .filter(
        (item): item is Record<string, unknown> =>
          Boolean(item) && typeof item === "object" && !Array.isArray(item)
      )
      .map((item) => ({
        name: typeof item.name === "string" ? item.name.trim() : "",
        valueFrom:
          typeof item.valueFrom === "string" &&
          isMCPValueReference(item.valueFrom)
            ? item.valueFrom.trim()
            : "",
      }))
  }
  if (typeof value !== "string") return []
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf("=")
      const name = (separator >= 0 ? line.slice(0, separator) : line).trim()
      const candidate = separator >= 0 ? line.slice(separator + 1).trim() : ""
      return {
        name,
        // Never retain a legacy plaintext value in editable client state.
        valueFrom: isMCPValueReference(candidate) ? candidate : "",
      }
    })
}

export function unsafeLegacyMCPHeaderNames(value: unknown): string[] {
  if (typeof value !== "string") return []
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .flatMap((line) => {
      const separator = line.indexOf("=")
      const name = (separator >= 0 ? line.slice(0, separator) : line).trim()
      const candidate = separator >= 0 ? line.slice(separator + 1).trim() : ""
      return isMCPValueReference(candidate) ? [] : [name || "未命名 Header"]
    })
}

function splitLegacyArgs(value: string) {
  const result: string[] = []
  let current = ""
  let quote: "'" | '"' | null = null
  let escaping = false
  let started = false

  for (const character of value) {
    if (escaping) {
      current += character
      escaping = false
      started = true
      continue
    }
    if (character === "\\" && quote !== "'") {
      escaping = true
      started = true
      continue
    }
    if (quote) {
      if (character === quote) quote = null
      else current += character
      started = true
      continue
    }
    if (character === "'" || character === '"') {
      quote = character
      started = true
      continue
    }
    if (/\s/.test(character)) {
      if (started) {
        result.push(current)
        current = ""
        started = false
      }
      continue
    }
    current += character
    started = true
  }
  if (escaping) current += "\\"
  if (started) result.push(current)
  return result
}
