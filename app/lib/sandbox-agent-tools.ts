import { z } from "zod"

import { supportedAgentToolIds, type AgentToolId } from "./agent-tools"
import { provisioningProgressSchema } from "./platform-schema"

export const sandboxAgentToolStatusSchema = z.enum([
  "installed",
  "not-installed",
  "broken",
  "updated",
  "unchanged",
  "failed",
])

export const sandboxAgentToolStateSchema = z.object({
  tool: z.enum(supportedAgentToolIds),
  currentVersion: z.string().default(""),
  latestVersion: z.string().default(""),
  previousVersion: z.string().default(""),
  status: sandboxAgentToolStatusSchema,
  message: z.string().default(""),
  source: z.string().default(""),
  checkedAt: z.string().datetime({ offset: true }),
})

export const sandboxAgentToolOperationSchema = z.object({
  action: z.enum(["check", "update"]),
  status: z.enum(["queued", "running", "succeeded", "failed"]),
  toolIds: z.array(z.enum(supportedAgentToolIds)),
  message: z.string().default(""),
  progress: provisioningProgressSchema.optional(),
  startedAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
  finishedAt: z.string().datetime({ offset: true }).optional(),
})

export type SandboxAgentToolState = z.infer<typeof sandboxAgentToolStateSchema>
export type SandboxAgentToolOperation = z.infer<
  typeof sandboxAgentToolOperationSchema
>

export function sandboxAgentToolStates(spec: Record<string, unknown>) {
  const result = z
    .array(sandboxAgentToolStateSchema)
    .safeParse(spec.agentToolVersions)
  return result.success ? result.data : []
}

export function sandboxAgentToolOperation(spec: Record<string, unknown>) {
  const result = sandboxAgentToolOperationSchema.safeParse(
    spec.agentToolOperation
  )
  return result.success ? result.data : null
}

export function agentToolNeedsUpdate(state: SandboxAgentToolState | undefined) {
  if (!state) return false
  if (["not-installed", "broken", "failed"].includes(state.status)) {
    return true
  }
  if (!state.currentVersion || !state.latestVersion) return false
  return compareSemanticVersions(state.currentVersion, state.latestVersion) < 0
}

export function compareSemanticVersions(left: string, right: string) {
  const leftVersion = parseSemanticVersion(left)
  const rightVersion = parseSemanticVersion(right)
  if (!leftVersion || !rightVersion) return 0
  for (let index = 0; index < 3; index += 1) {
    const difference = leftVersion.core[index] - rightVersion.core[index]
    if (difference !== 0) return Math.sign(difference)
  }
  if (leftVersion.pre.length === 0 && rightVersion.pre.length === 0) return 0
  if (leftVersion.pre.length === 0) return 1
  if (rightVersion.pre.length === 0) return -1
  const length = Math.max(leftVersion.pre.length, rightVersion.pre.length)
  for (let index = 0; index < length; index += 1) {
    const leftPart = leftVersion.pre[index]
    const rightPart = rightVersion.pre[index]
    if (leftPart === undefined) return -1
    if (rightPart === undefined) return 1
    if (leftPart === rightPart) continue
    const leftNumber = /^\d+$/.test(leftPart) ? Number(leftPart) : null
    const rightNumber = /^\d+$/.test(rightPart) ? Number(rightPart) : null
    if (leftNumber !== null && rightNumber !== null) {
      return Math.sign(leftNumber - rightNumber)
    }
    if (leftNumber !== null) return -1
    if (rightNumber !== null) return 1
    return leftPart.localeCompare(rightPart)
  }
  return 0
}

function parseSemanticVersion(value: string) {
  const match = value
    .trim()
    .match(/^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/)
  if (!match) return null
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    pre: match[4]?.split(".") ?? [],
  }
}

export function agentToolLabel(
  tool: AgentToolId,
  options: readonly { value: AgentToolId; label: string }[]
) {
  return options.find((option) => option.value === tool)?.label ?? tool
}
