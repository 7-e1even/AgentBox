import type { AppSection } from "./app-section"

export type ConsoleDomain =
  "resources" | "servers" | "credentials" | "proxies" | "catalog" | "users"

const sectionDomains: Record<AppSection, ConsoleDomain[]> = {
  overview: ["resources", "servers", "credentials"],
  projects: ["resources"],
  sandboxes: ["resources", "servers", "proxies"],
  automations: ["resources", "credentials"],
  automationRuns: [],
  runtimes: ["resources", "servers"],
  servers: ["servers", "resources"],
  images: ["servers"],
  access: ["credentials", "catalog"],
  proxies: ["proxies", "servers"],
  extensions: ["resources"],
  settings: ["resources"],
  mcp: ["resources"],
  skills: ["resources"],
  variables: ["resources"],
  logs: [],
  users: ["users"],
}

export function domainsForSection(
  section: AppSection | undefined,
  isAdmin: boolean
): ConsoleDomain[] {
  if (!section || (section === "users" && !isAdmin)) return []
  return sectionDomains[section]
}

export function domainsForEditor(kind: string | undefined): ConsoleDomain[] {
  if (!kind || kind === "project") return []
  return kind === "runtime" || kind === "sandbox"
    ? ["resources", "servers", "credentials", "proxies"]
    : ["resources"]
}

export function editorNeedsAllProjectResources(kind: string | undefined) {
  return Boolean(
    kind && !["project", "image", "runtime", "sandbox"].includes(kind)
  )
}
