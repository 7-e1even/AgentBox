import type { Resource, ResourceOfKind } from "@/lib/platform-schema"

export type SandboxExtension = ResourceOfKind<"extension">

export function sandboxExtensions(resources: Resource[]): SandboxExtension[] {
  return resources.filter(
    (resource): resource is SandboxExtension => resource.kind === "extension"
  )
}

export function extensionMatchesQuery(
  extension: SandboxExtension,
  query: string
) {
  return `${extension.name} ${extension.id} ${extension.description}`
    .toLocaleLowerCase()
    .includes(query.trim().toLocaleLowerCase())
}

export function extensionSourceLabel(source?: string) {
  return source === "preset" ? "预设" : "自定义"
}

export function extensionSelectionOptions(
  extensions: SandboxExtension[],
  selectedIds: string[]
) {
  const options: Array<{ id: string; extension?: SandboxExtension }> =
    extensions.map((extension) => ({ id: extension.id, extension }))
  const known = new Set(extensions.map((extension) => extension.id))
  for (const id of new Set(selectedIds)) {
    if (!known.has(id)) options.push({ id })
  }
  return options
}

export function filterExtensionSelectionOptions(
  options: ReturnType<typeof extensionSelectionOptions>,
  selectedIds: string[],
  query: string,
  selectedOnly: boolean
) {
  const selected = new Set(selectedIds)
  return options.filter(
    ({ id, extension }) =>
      (!selectedOnly || selected.has(id)) &&
      (extension
        ? extensionMatchesQuery(extension, query)
        : id.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  )
}
