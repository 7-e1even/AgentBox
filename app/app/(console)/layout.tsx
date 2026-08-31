import { cookies } from "next/headers"
import { redirect } from "next/navigation"

import { ControlPlaneShell } from "@/components/control-plane-shell"
import { BackendUnavailable } from "@/components/backend-unavailable"
import { resourcesResponseSchema } from "@/lib/platform-schema"
import { PROJECT_COOKIE_NAME, resolveProjectId } from "@/lib/project-scope"
import { userResponseSchema } from "@/lib/user-schema"

export default async function ConsoleLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"
  const cookieStore = await cookies()
  const options: RequestInit = {
    cache: "no-store",
    headers: { Cookie: cookieStore.toString() },
  }
  const sessionResponse = await fetch(
    `${apiOrigin}/api/auth/me`,
    options
  ).catch(() => null)
  if (!sessionResponse) return <BackendUnavailable variant="unreachable" />
  if (sessionResponse.status === 401) redirect("/login")
  if (!sessionResponse.ok) return <BackendUnavailable variant="error" />
  const session = userResponseSchema.safeParse(
    await sessionResponse.json().catch(() => null)
  )
  if (!session.success) return <BackendUnavailable variant="error" />

  // Only navigation data belongs to the persistent layout. A domain outage
  // must not prevent unrelated console pages from rendering.
  const projectsResponse = await fetch(
    `${apiOrigin}/api/resources?kind=project`,
    options
  ).catch(() => null)
  if (projectsResponse?.status === 401) redirect("/login")
  const projects = projectsResponse?.ok
    ? resourcesResponseSchema.safeParse(
        await projectsResponse.json().catch(() => null)
      )
    : null
  const initialProjects = projects?.success
    ? projects.data.resources
    : undefined
  const preferredProjectId = cookieStore.get(PROJECT_COOKIE_NAME)?.value
  return (
    <ControlPlaneShell
      initialProjects={initialProjects}
      currentUser={session.data.user}
      initialProjectId={
        initialProjects
          ? resolveProjectId(initialProjects, preferredProjectId)
          : preferredProjectId || "default"
      }
    >
      {children}
    </ControlPlaneShell>
  )
}
