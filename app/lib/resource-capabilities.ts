import type { Resource, ResourceOfKind } from "./platform-schema"

export type CapabilityReferenceKey = "skillIds" | "mcpServerIds" | "variableIds"

export function capabilityUsage(
  resources: Resource[],
  resourceId: string,
  key: CapabilityReferenceKey
) {
  let templates = 0
  let sandboxes = 0
  for (const resource of resources) {
    if (
      (resource.kind !== "runtime" && resource.kind !== "sandbox") ||
      !stringList(resource.spec[key]).includes(resourceId)
    ) {
      continue
    }
    if (resource.kind === "runtime") templates += 1
    else sandboxes += 1
  }
  return { templates, sandboxes }
}

export function capabilityUsageLabel({
  templates,
  sandboxes,
}: {
  templates: number
  sandboxes: number
}) {
  const parts = [
    templates > 0 ? `${templates} 个模板` : "",
    sandboxes > 0 ? `${sandboxes} 个沙箱` : "",
  ].filter(Boolean)
  return parts.join(" · ") || "未使用"
}

export function sandboxCapabilitySelection(
  sandbox: ResourceOfKind<"sandbox">,
  environment: ResourceOfKind<"runtime"> | undefined,
  key: CapabilityReferenceKey
) {
  if (Object.hasOwn(sandbox.spec, key)) {
    return { ids: stringList(sandbox.spec[key]), legacyTemplateFallback: false }
  }
  return {
    ids: stringList(environment?.spec[key]),
    legacyTemplateFallback: true,
  }
}

export function sandboxConfigurationState(
  sandbox: ResourceOfKind<"sandbox">
): "applied" | "applying" | "restart-required" {
  if (sandbox.spec.capabilitiesPendingRestart === true) {
    return ["requested", "starting", "restarting"].includes(
      String(sandbox.spec.status ?? "")
    )
      ? "applying"
      : "restart-required"
  }
  if (sandbox.observedGeneration >= sandbox.generation) return "applied"
  if (
    ["requested", "starting", "restarting"].includes(
      String(sandbox.spec.status ?? "")
    )
  ) {
    return "applying"
  }
  return "restart-required"
}

function stringList(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}
