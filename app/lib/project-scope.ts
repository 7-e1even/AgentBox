import type { Resource } from "@/lib/platform-schema"

export const PROJECT_COOKIE_NAME = "agentbox-project"

export function resolveProjectId(
  resources: Resource[],
  preferredProjectId?: string | null
) {
  const projects = resources.filter((resource) => resource.kind === "project")
  return (
    projects.find((project) => project.id === preferredProjectId)?.id ??
    projects[0]?.id ??
    "default"
  )
}

export function resourcesForProject(resources: Resource[], projectId: string) {
  return resources.filter((resource) => {
    if (resource.kind === "image") return true
    if (resource.kind === "project") return resource.id === projectId
    return resource.projectId === projectId
  })
}
