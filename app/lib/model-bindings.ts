import type { ManagedCredential } from "./credential-schema"

export function reconcileModelBindings(
  credentialIds: string[],
  credentials: ManagedCredential[],
  current: Record<string, string> = {}
) {
  const credentialsById = new Map(
    credentials.map((credential) => [credential.id, credential])
  )

  return Object.fromEntries(
    Array.from(new Set(credentialIds)).flatMap((credentialId) => {
      const credential = credentialsById.get(credentialId)
      if (!credential?.enabled) return []

      const modelIds = Array.from(
        new Set(
          credential.models
            .map((model) => model.id.trim())
            .filter((modelId) => modelId.length > 0)
        )
      )
      const selectedModelId = current[credentialId]?.trim()
      const modelId = modelIds.includes(selectedModelId)
        ? selectedModelId
        : modelIds.length === 1
          ? modelIds[0]
          : ""

      return modelId ? [[credentialId, modelId]] : []
    })
  )
}
