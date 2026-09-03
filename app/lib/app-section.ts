export const APP_SECTION_PATHS = {
  overview: "/overview",
  projects: "/projects",
  sandboxes: "/sandboxes",
  automations: "/automations",
  automationRuns: "/automations/runs",
  servers: "/servers",
  runtimes: "/environment-templates",
  images: "/images",
  skills: "/skills",
  mcp: "/mcp-servers",
  access: "/model-services",
  proxies: "/network-proxies",
  extensions: "/sandbox-extensions",
  settings: "/settings",
  variables: "/variables",
  users: "/users",
  logs: "/logs",
} as const

export type AppSection = keyof typeof APP_SECTION_PATHS

export function appSectionPath(section: AppSection) {
  return APP_SECTION_PATHS[section]
}
