export const APP_SECTION_PATHS = {
  overview: "/overview",
  projects: "/projects",
  automations: "/automations",
  agents: "/agents",
  sandboxes: "/sandboxes",
  servers: "/servers",
  runtimes: "/environment-templates",
  images: "/images",
  skills: "/skills",
  mcp: "/mcp-servers",
  access: "/model-services",
  settings: "/settings",
  variables: "/variables",
  users: "/users",
} as const

export type AppSection = keyof typeof APP_SECTION_PATHS

export function appSectionPath(section: AppSection) {
  return APP_SECTION_PATHS[section]
}
