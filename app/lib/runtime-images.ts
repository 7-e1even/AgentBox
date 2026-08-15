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
  return (
    driver === "docker" ||
    driver === "boxlite" ||
    driver === "microsandbox" ||
    driver === "vm"
  )
}

export function runtimeInventoryImages(
  server: ManagedServer | undefined,
  driver: string
): ServerImage[] {
  if (!server) return []
  if (
    driver === "docker" ||
    driver === "boxlite" ||
    driver === "microsandbox"
  ) {
    return sharedOCIImages(server)
  }
  if (driver === "vm") return server.inventory.vmImages
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
  if (driver === "vm") return { local, registry: [] }

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
      label: `${reference} · ${driver === "microsandbox" ? "创建时导入或拉取" : "创建时拉取"}`,
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
  if (
    driver === "docker" ||
    driver === "boxlite" ||
    driver === "microsandbox"
  ) {
    return (
      runtimeInventoryImages(server, driver)[0]?.reference ??
      defaultOCIReference
    )
  }
  return ""
}

function sharedOCIImages(server: ManagedServer): ServerImage[] {
  const images = [
    ...server.inventory.dockerImages.map((image) => ({ image, fallback: 4 })),
    ...server.inventory.boxliteImages.map((image) => ({ image, fallback: 2 })),
    ...server.inventory.microsandboxImages.map((image) => ({
      image,
      fallback: 1,
    })),
  ]
  const unique = new Map<string, { image: ServerImage; priority: number }>()

  for (const { image, fallback } of images) {
    const reference = image.reference.trim()
    const key =
      reference && reference !== "<none>:<none>"
        ? reference
        : image.id || `${image.source}-${image.path}`
    const current = unique.get(key)
    const priority = imageSourcePriority(image, fallback)
    if (!current || priority > current.priority) {
      unique.set(key, { image, priority })
    }
  }

  return [...unique.values()].map(({ image }) => image)
}

function imageSourcePriority(image: ServerImage, fallback: number) {
  if (image.source === "docker-local") return 4
  if (image.source === "worker-oci") return 3
  if (image.source === "registry-cache") return 2
  if (image.source === "runtime-cache") return 1
  return fallback
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : ""
}
