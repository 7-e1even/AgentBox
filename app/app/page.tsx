import { BackendUnavailable } from "@/components/backend-unavailable"
import { AgentManagement } from "@/components/agent-management"
import { agentsResponseSchema } from "@/lib/agent-schema"
import { catalogSchema } from "@/lib/catalog"
import { resourcesResponseSchema } from "@/lib/platform-schema"

export const dynamic = "force-dynamic"

async function loadPlatform() {
  const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"
  const [agentsResponse, catalogResponse, resourcesResponse] =
    await Promise.all([
      fetch(`${apiOrigin}/api/agents`, { cache: "no-store" }),
      fetch(`${apiOrigin}/api/catalog`, { cache: "no-store" }),
      fetch(`${apiOrigin}/api/resources`, { cache: "no-store" }),
    ])
  if (!agentsResponse.ok || !catalogResponse.ok || !resourcesResponse.ok) {
    throw new Error("Go API returned an error")
  }
  const agentsBody = agentsResponseSchema.parse(await agentsResponse.json())
  const catalog = catalogSchema.parse(await catalogResponse.json())
  const resources = resourcesResponseSchema.parse(
    await resourcesResponse.json()
  )
  return { agents: agentsBody.agents, catalog, resources: resources.resources }
}

async function resolvePlatform() {
  try {
    return await loadPlatform()
  } catch {
    return null
  }
}

export default async function Home() {
  const platform = await resolvePlatform()
  if (!platform) return <BackendUnavailable />
  return (
    <AgentManagement
      initialAgents={platform.agents}
      catalog={platform.catalog}
      initialResources={platform.resources}
    />
  )
}
