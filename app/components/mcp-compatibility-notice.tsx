import { PlugZapIcon, TriangleAlertIcon } from "lucide-react"

import { agentToolOptions } from "@/lib/agent-tools"
import {
  mcpAgentToolCompatibility,
  mcpSupportedAgentToolIds,
} from "@/lib/mcp-config"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

const labels = new Map<string, string>(
  agentToolOptions.map((option) => [option.value, option.label] as const)
)

export function MCPCompatibilityNotice({
  agentTools,
  mcpServerIds,
}: {
  agentTools: unknown
  mcpServerIds: unknown
}) {
  const selected = Array.isArray(mcpServerIds) && mcpServerIds.length > 0
  const compatibility = mcpAgentToolCompatibility(agentTools)
  const supportedLabels = compatibility.supported.map(label)
  const unsupportedLabels = compatibility.unsupported.map(label)

  if (!selected) {
    return (
      <p className="text-xs text-muted-foreground">
        MCP 当前支持 {mcpSupportedAgentToolIds.map(label).join("、")}；其他
        Agent 不会注入 MCP 配置。
      </p>
    )
  }

  if (compatibility.supported.length === 0) {
    return (
      <Alert variant="destructive">
        <TriangleAlertIcon />
        <AlertTitle>所选 Agent 无法使用 MCP</AlertTitle>
        <AlertDescription>
          {unsupportedLabels.length > 0
            ? `${unsupportedLabels.join("、")} 当前都不支持 MCP；请增加一个受支持的 Agent，或取消 MCP。`
            : "尚未选择可接收 MCP 配置的 Agent。"}
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <Alert>
      <PlugZapIcon />
      <AlertTitle>MCP 将注入 {supportedLabels.join("、")}</AlertTitle>
      <AlertDescription>
        {unsupportedLabels.length > 0
          ? `${unsupportedLabels.join("、")} 不支持 MCP，不会收到这部分配置。`
          : "当前所选 Agent 均支持 MCP。"}
      </AlertDescription>
    </Alert>
  )
}

function label(id: string) {
  return labels.get(id) ?? id
}
