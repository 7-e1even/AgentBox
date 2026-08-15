import type { ManagedServer, ServerImage } from "./server-schema"

export type RuntimeDriver = "docker" | "boxlite" | "microsandbox" | "vm"

export type RuntimeImageChoice = {
  value: string
  label: string
}

export type RuntimeImageChoices = {
  local: RuntimeImageChoice[]
  registry: RuntimeImageChoice[]
}

const defaultOCIReference = "ubuntu:24.04"

export function usesRuntimeImageInventory(driver: string) {
  return driver === "docker" || driver === "vm"
}

export function runtimeInventoryImages(
  server: ManagedServer | undefined,
  driver: string
): ServerImage[] {
  if (driver === "docker") return server?.inventory.dockerImages ?? []
  if (driver === "vm") return server?.inventory.vmImages ?? []
  return []
}

export function runtimeImageChoices(
  server: ManagedServer | undefined,
  driver: string,
  current: unknown
): RuntimeImageChoices {
  const images = runtimeInventoryImages(server, driver)
  const local = images.map((image) => ({
    value: image.reference,
    label: `${image.reference}${image.size ? ` · ${image.size}` : ""}`,
  }))
  if (driver !== "docker") return { local, registry: [] }

  const registry: RuntimeImageChoice[] = []
  const currentReference = stringValue(current).trim()
  for (const reference of [currentReference, defaultOCIReference]) {
    if (
      !reference ||
      local.some((option) => option.value === reference) ||
      registry.some((option) => option.value === reference)
    ) {
      continue
    }
    registry.push({
      value: reference,
      label: `${reference} · 创建时拉取`,
    })
  }
  return { local, registry }
}

export function normalizeRuntimeImageReference(
  server: ManagedServer | undefined,
  driver: string,
  current: unknown
) {
  const currentReference = stringValue(current).trim()
  if (driver === "vm") {
    const images = runtimeInventoryImages(server, driver)
    const currentImage = images.find((image) =>
      [image.reference, image.path, image.id].includes(currentReference)
    )
    return currentImage?.reference ?? images[0]?.reference ?? ""
  }
  if (currentReference) return currentReference
  if (driver === "docker") {
    return server?.inventory.dockerImages[0]?.reference ?? defaultOCIReference
  }
  if (driver === "boxlite" || driver === "microsandbox") {
    return defaultOCIReference
  }
  return ""
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : ""
}
