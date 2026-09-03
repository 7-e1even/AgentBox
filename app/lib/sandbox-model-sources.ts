import type { ManagedCredential } from "./credential-schema"
import type { RuntimeModelSource } from "./platform-schema"

export type RuntimeModelSourceSlot = {
  slotCredentialId: string
  slotName: string
  source: Pick<RuntimeModelSource, "credentialId" | "modelId"> & {
    updatedAt: string | null
  }
  recorded: boolean
}

export type ModelSourceBaseline = {
  credentialIds?: string[] | null
  modelBindings?: Record<string, string> | null
}

export type ModelSourceOption = {
  value: string
  label: string
  credentialId: string
  credentialName: string
  modelId: string
  modelName: string
  protocol: ManagedCredential["protocol"]
}

export function runtimeModelSourceSlots(
  sources: Record<string, RuntimeModelSource> | null | undefined,
  baseline: ModelSourceBaseline,
  credentials: ManagedCredential[],
  sourcesComplete = false
): RuntimeModelSourceSlot[] {
  const credentialsById = new Map(
    credentials.map((credential) => [credential.id, credential])
  )
  const modelBindings = baseline.modelBindings ?? {}
  const baselineCredentialIds =
    baseline.credentialIds ?? Object.keys(modelBindings)
  const slots = new Map<string, RuntimeModelSourceSlot>()

  if (!sourcesComplete) {
    for (const slotCredentialId of new Set(baselineCredentialIds)) {
      const modelId = modelBindings[slotCredentialId]?.trim()
      if (!slotCredentialId || !modelId) continue
      slots.set(slotCredentialId, {
        slotCredentialId,
        slotName:
          credentialsById.get(slotCredentialId)?.name.trim() ||
          slotCredentialId,
        source: { credentialId: slotCredentialId, modelId, updatedAt: null },
        recorded: false,
      })
    }
  }

  for (const [slotCredentialId, source] of Object.entries(sources ?? {})) {
    slots.set(slotCredentialId, {
      slotCredentialId,
      slotName:
        credentialsById.get(slotCredentialId)?.name.trim() || slotCredentialId,
      source,
      recorded: true,
    })
  }

  return Array.from(slots.values()).sort(
    (left, right) =>
      left.slotName.localeCompare(right.slotName, "zh-CN") ||
      left.slotCredentialId.localeCompare(right.slotCredentialId)
  )
}

export function modelSourceOptions(
  credentials: ManagedCredential[]
): ModelSourceOption[] {
  const seen = new Set<string>()
  return credentials.flatMap((credential) => {
    if (!credential.enabled) return []

    return credential.models.flatMap((model) => {
      const modelId = model.id.trim()
      const key = modelSourceKey({ credentialId: credential.id, modelId })
      if (!modelId || seen.has(key)) return []
      seen.add(key)

      const credentialName = credential.name.trim() || credential.id
      const modelName = model.name.trim() || modelId
      return [
        {
          value: key,
          label: sameDisplayText(modelName, modelId)
            ? `${credentialName} · ${modelName}`
            : `${credentialName} · ${modelName} · ${modelId}`,
          credentialId: credential.id,
          credentialName,
          modelId,
          modelName,
          protocol: credential.protocol,
        },
      ]
    })
  })
}

export function filterModelSourceOptions(
  options: ModelSourceOption[],
  query: string
) {
  const terms = normalizeSearchText(query).split(/\s+/).filter(Boolean)
  if (terms.length === 0) return options

  return options.filter((option) => {
    const searchableText = normalizeSearchText(
      [
        option.credentialName,
        option.credentialId,
        option.modelName,
        option.modelId,
        option.protocol,
      ].join(" ")
    )
    return terms.every((term) => searchableText.includes(term))
  })
}

export function findModelSourceOption(
  options: ModelSourceOption[],
  source: Pick<RuntimeModelSource, "credentialId" | "modelId"> | null
) {
  if (!source) return null
  return (
    options.find(
      (option) =>
        option.credentialId === source.credentialId &&
        option.modelId === source.modelId
    ) ?? null
  )
}

export function describeModelSource(
  source: Pick<RuntimeModelSource, "credentialId" | "modelId">,
  credentials: ManagedCredential[]
) {
  const credential = credentials.find((item) => item.id === source.credentialId)
  const model = credential?.models.find(
    (item) => item.id.trim() === source.modelId
  )
  const credentialName = credential?.name.trim() || source.credentialId
  const modelName = model?.name.trim() || source.modelId
  return sameDisplayText(modelName, source.modelId)
    ? `${credentialName} · ${modelName}`
    : `${credentialName} · ${modelName} · ${source.modelId}`
}

export function sameModelSource(
  left: Pick<RuntimeModelSource, "credentialId" | "modelId"> | null,
  right: Pick<RuntimeModelSource, "credentialId" | "modelId"> | null
) {
  return (
    left?.credentialId === right?.credentialId &&
    left?.modelId === right?.modelId
  )
}

function modelSourceKey(
  source: Pick<RuntimeModelSource, "credentialId" | "modelId">
) {
  return JSON.stringify([source.credentialId, source.modelId])
}

function sameDisplayText(left: string, right: string) {
  return left.toLowerCase() === right.toLowerCase()
}

function normalizeSearchText(value: string) {
  return value.normalize("NFKC").trim().toLowerCase()
}
