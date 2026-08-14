import { cookies } from "next/headers"
import { redirect } from "next/navigation"

import { AgentManagement } from "@/components/agent-management"
import { BackendUnavailable } from "@/components/backend-unavailable"
import { agentsResponseSchema } from "@/lib/agent-schema"
import { catalogSchema } from "@/lib/catalog"
import { credentialsResponseSchema } from "@/lib/credential-schema"
import { resourcesResponseSchema } from "@/lib/platform-schema"
import { PROJECT_COOKIE_NAME, resolveProjectId } from "@/lib/project-scope"
import { serversResponseSchema } from "@/lib/server-schema"
import { userResponseSchema, usersResponseSchema } from "@/lib/user-schema"

async function loadPlatform(cookieHeader: string, isAdmin: boolean) {
  const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"
  const [
    agentsResponse,
    catalogResponse,
    resourcesResponse,
    serversResponse,
    credentialsResponse,
  ] = await Promise.all([
    fetch(`${apiOrigin}/api/agents`, requestOptions(cookieHeader)),
    fetch(`${apiOrigin}/api/catalog`, requestOptions(cookieHeader)),
    fetch(`${apiOrigin}/api/resources`, requestOptions(cookieHeader)),
    fetch(`${apiOrigin}/api/servers`, requestOptions(cookieHeader)),
    fetch(`${apiOrigin}/api/credentials`, requestOptions(cookieHeader)),
  ])
  if (
    !agentsResponse.ok ||
    !catalogResponse.ok ||
    !resourcesResponse.ok ||
    !serversResponse.ok ||
    !credentialsResponse.ok
  ) {
    throw new Error("Go API returned an error")
  }
  const agentsBody = agentsResponseSchema.parse(await agentsResponse.json())
  const catalog = catalogSchema.parse(await catalogResponse.json())
  const resources = resourcesResponseSchema.parse(
    await resourcesResponse.json()
  )
  const servers = serversResponseSchema.parse(await serversResponse.json())
  const credentials = credentialsResponseSchema.parse(
    await credentialsResponse.json()
  )
  const users = isAdmin
    ? usersResponseSchema.parse(
        await fetch(
          `${apiOrigin}/api/users`,
          requestOptions(cookieHeader)
        ).then((response) => {
          if (!response.ok) throw new Error("Go API returned an error")
          return response.json()
        })
      ).users
    : []
  return {
    agents: agentsBody.agents,
    catalog,
    resources: resources.resources,
    servers: servers.servers,
    credentials: credentials.credentials,
    users,
  }
}

function requestOptions(cookieHeader: string): RequestInit {
  return {
    cache: "no-store",
    headers: { Cookie: cookieHeader },
  }
}

async function resolvePlatform(cookieHeader: string, isAdmin: boolean) {
  try {
    return await loadPlatform(cookieHeader, isAdmin)
  } catch {
    return null
  }
}

export default async function ConsoleLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"
  const cookieStore = await cookies()
  const cookieHeader = cookieStore.toString()
  const sessionResponse = await fetch(
    `${apiOrigin}/api/auth/me`,
    requestOptions(cookieHeader)
  ).catch(() => null)
  if (!sessionResponse) return <BackendUnavailable />
  if (sessionResponse.status === 401) redirect("/login")
  if (!sessionResponse.ok) return <BackendUnavailable />
  const currentUser = userResponseSchema.parse(
    await sessionResponse.json()
  ).user
  const platform = await resolvePlatform(
    cookieHeader,
    currentUser.role === "admin"
  )
  if (!platform) return <BackendUnavailable />
  const initialProjectId = resolveProjectId(
    platform.resources,
    cookieStore.get(PROJECT_COOKIE_NAME)?.value
  )

  return (
    <AgentManagement
      initialAgents={platform.agents}
      catalog={platform.catalog}
      initialResources={platform.resources}
      initialServers={platform.servers}
      initialCredentials={platform.credentials}
      currentUser={currentUser}
      initialUsers={platform.users}
      initialProjectId={initialProjectId}
    >
      {children}
    </AgentManagement>
  )
}
