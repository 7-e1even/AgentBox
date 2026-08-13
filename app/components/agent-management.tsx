"use client"

import { useMemo, useState } from "react"
import { useTheme } from "next-themes"
import {
  ArchiveIcon,
  BotIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CircleDotIcon,
  CopyIcon,
  MoreHorizontalIcon,
  MoonIcon,
  PencilIcon,
  PlugZapIcon,
  PlusIcon,
  RotateCcwIcon,
  SearchIcon,
  SparklesIcon,
  SunIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { AgentEditorDialog } from "@/components/agent-editor-dialog"
import { AppSidebar } from "@/components/app-sidebar"
import { OverviewView, ResourceView } from "@/components/control-plane-view"
import { ResourceEditorDialog } from "@/components/resource-editor-dialog"
import {
  agentResponseSchema,
  type Agent,
  type AgentInput,
  type AgentStatus,
} from "@/lib/agent-schema"
import type {
  Credential,
  McpServerDefinition,
  Provider,
  SkillDefinition,
} from "@/lib/catalog"
import {
  resourceResponseSchema,
  type Resource,
  type ResourceInput,
  type ResourceKind,
} from "@/lib/platform-schema"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

export type AppSection =
  | "overview"
  | "projects"
  | "agents"
  | "runtimes"
  | "sandboxes"
  | "skills"
  | "mcp"
  | "schedules"
  | "webhooks"
  | "variables"

const sectionKinds: Partial<Record<AppSection, ResourceKind>> = {
  projects: "project",
  runtimes: "runtime",
  sandboxes: "sandbox",
  skills: "skill",
  mcp: "mcp",
  schedules: "schedule",
  webhooks: "webhook",
  variables: "variable",
}

type Catalog = {
  project: { id: string; name: string }
  providers: Provider[]
  credentials: Credential[]
  skills: SkillDefinition[]
  mcpServers: McpServerDefinition[]
}

const statusMeta: Record<AgentStatus, { label: string; className: string }> = {
  active: {
    label: "已启用",
    className:
      "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300",
  },
  draft: {
    label: "草稿",
    className:
      "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300",
  },
  archived: {
    label: "已归档",
    className: "border-border bg-muted text-muted-foreground",
  },
}

function agentInput(agent: Agent): AgentInput {
  return {
    projectId: agent.projectId,
    runtimeId: agent.runtimeId,
    name: agent.name,
    slug: agent.slug,
    description: agent.description,
    avatar: agent.avatar,
    providerId: agent.providerId,
    modelId: agent.modelId,
    credentialId: agent.credentialId,
    systemPrompt: agent.systemPrompt,
    skillIds: agent.skillIds,
    mcpServerIds: agent.mcpServerIds,
    variableIds: agent.variableIds,
    customArgs: agent.customArgs,
    temperature: agent.temperature,
    maxSteps: agent.maxSteps,
    concurrency: agent.concurrency,
    sandboxPolicy: agent.sandboxPolicy,
    status: agent.status,
  }
}

function ordered(agents: Agent[]) {
  const weight: Record<AgentStatus, number> = {
    active: 0,
    draft: 1,
    archived: 2,
  }
  return [...agents].sort(
    (a, b) =>
      weight[a.status] - weight[b.status] ||
      b.updatedAt.localeCompare(a.updatedAt)
  )
}

async function requestJson<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => ({ error: "请求失败" }))) as { error?: string }
    throw new Error(body.error || "请求失败")
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

function updatedLabel(value: string) {
  const date = new Date(value)
  const diff = Date.now() - date.getTime()
  const minutes = Math.floor(diff / 60_000)
  if (minutes < 1) return "刚刚更新"
  if (minutes < 60) return `${minutes} 分钟前更新`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前更新`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days} 天前更新`
  return `${date.toLocaleDateString("zh-CN")} 更新`
}

export function AgentManagement({
  initialAgents,
  initialResources,
  catalog,
}: {
  initialAgents: Agent[]
  initialResources: Resource[]
  catalog: Catalog
}) {
  const [agents, setAgents] = useState(() => ordered(initialAgents))
  const [resources, setResources] = useState(initialResources)
  const [section, setSection] = useState<AppSection>("overview")
  const [projectId, setProjectId] = useState(
    initialResources.find((item) => item.kind === "project")?.id ?? "default"
  )
  const [editor, setEditor] = useState<{ open: boolean; agent: Agent | null }>({
    open: false,
    agent: null,
  })
  const [deleting, setDeleting] = useState<Agent | null>(null)
  const [resourceEditor, setResourceEditor] = useState<{
    kind: ResourceKind
    resource: Resource | null
  } | null>(null)
  const [deletingResource, setDeletingResource] = useState<Resource | null>(
    null
  )
  const [busyId, setBusyId] = useState<string | null>(null)
  const { setTheme } = useTheme()

  const count = (kind: ResourceKind) =>
    resources.filter((item) => item.kind === kind).length
  const counts: Record<AppSection, number> = {
    overview: 0,
    projects: count("project"),
    agents: agents.filter((item) => item.projectId === projectId).length,
    runtimes: count("runtime"),
    sandboxes: count("sandbox"),
    skills: count("skill"),
    mcp: count("mcp"),
    schedules: count("schedule"),
    webhooks: count("webhook"),
    variables: count("variable"),
  }
  const sectionLabels: Record<AppSection, string> = {
    overview: "概览",
    projects: "Projects",
    agents: "Agents",
    runtimes: "Runtimes",
    sandboxes: "Sandboxes",
    skills: "Skills",
    mcp: "MCP Servers",
    schedules: "Schedules",
    webhooks: "Webhooks",
    variables: "Variables & Secrets",
  }

  const projectResources = resources.filter((item) => item.kind === "project")
  const agentCatalog = {
    ...catalog,
    projects: [...projectResources].sort((a, b) =>
      a.id === projectId
        ? -1
        : b.id === projectId
          ? 1
          : a.name.localeCompare(b.name)
    ),
    runtimes: resources.filter(
      (item) => item.kind === "runtime" && item.enabled
    ),
    variables: resources.filter(
      (item) => item.kind === "variable" && item.enabled
    ),
    skills: resources
      .filter((item) => item.kind === "skill" && item.enabled)
      .map((item) => ({
        id: item.id,
        name: item.name,
        description: item.description,
        version: String(item.spec.version ?? "1.0.0"),
        category: String(item.spec.category ?? "通用"),
      })),
    mcpServers: resources
      .filter((item) => item.kind === "mcp")
      .map((item) => ({
        id: item.id,
        name: item.name,
        description: item.description,
        transport:
          item.spec.transport === "http"
            ? ("http" as const)
            : ("stdio" as const),
        toolCount: Number(item.spec.toolCount ?? 0),
        status: item.enabled ? ("ready" as const) : ("attention" as const),
      })),
  }

  async function save(input: AgentInput, editing: Agent | null) {
    const body = editing ? { ...input, version: editing.version } : input
    const result = agentResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/agents/${editing.id}` : "/api/agents",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(body),
        }
      )
    )
    setAgents((current) =>
      ordered(
        editing
          ? current.map((item) =>
              item.id === result.agent.id ? result.agent : item
            )
          : [result.agent, ...current]
      )
    )
    setEditor({ open: false, agent: null })
    toast.success(editing ? "Agent 已更新" : "Agent 已创建", {
      description: `${result.agent.name} · ${statusMeta[result.agent.status].label}`,
    })
  }

  async function changeStatus(agent: Agent, status: AgentStatus) {
    setBusyId(agent.id)
    try {
      const result = agentResponseSchema.parse(
        await requestJson<unknown>(`/api/agents/${agent.id}`, {
          method: "PATCH",
          body: JSON.stringify({
            ...agentInput(agent),
            status,
            version: agent.version,
          }),
        })
      )
      setAgents((current) =>
        ordered(
          current.map((item) => (item.id === agent.id ? result.agent : item))
        )
      )
      toast.success(
        status === "archived"
          ? "Agent 已归档"
          : status === "active"
            ? "Agent 已启用"
            : "Agent 已转为草稿"
      )
    } catch (error) {
      toast.error("状态更新失败", {
        description: error instanceof Error ? error.message : "请稍后重试",
      })
    } finally {
      setBusyId(null)
    }
  }

  async function duplicate(agent: Agent) {
    setBusyId(agent.id)
    try {
      const result = agentResponseSchema.parse(
        await requestJson<unknown>(`/api/agents/${agent.id}/duplicate`, {
          method: "POST",
        })
      )
      setAgents((current) => ordered([result.agent, ...current]))
      toast.success("已创建草稿副本", { description: result.agent.name })
    } catch (error) {
      toast.error("复制失败", {
        description: error instanceof Error ? error.message : "请稍后重试",
      })
    } finally {
      setBusyId(null)
    }
  }

  async function permanentlyDelete() {
    if (!deleting) return
    const target = deleting
    setBusyId(target.id)
    try {
      await requestJson<void>(`/api/agents/${target.id}`, { method: "DELETE" })
      setAgents((current) => current.filter((item) => item.id !== target.id))
      setDeleting(null)
      toast.success("Agent 已永久删除", { description: target.name })
    } catch (error) {
      toast.error("删除失败", {
        description: error instanceof Error ? error.message : "请稍后重试",
      })
    } finally {
      setBusyId(null)
    }
  }

  async function saveResource(input: ResourceInput) {
    const editing = resourceEditor?.resource
    const result = resourceResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/resources/${editing.id}` : "/api/resources",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(input),
        }
      )
    )
    setResources((current) =>
      editing
        ? current.map((item) =>
            item.id === result.resource.id ? result.resource : item
          )
        : [...current, result.resource]
    )
    setResourceEditor(null)
    if (input.kind === "project" && !editing) setProjectId(input.id)
    toast.success(editing ? "配置已更新" : "配置已创建", {
      description: result.resource.name,
    })
  }

  async function permanentlyDeleteResource() {
    if (!deletingResource) return
    try {
      await requestJson<void>(`/api/resources/${deletingResource.id}`, {
        method: "DELETE",
      })
      setResources((current) =>
        current.filter((item) => item.id !== deletingResource.id)
      )
      if (
        deletingResource.kind === "project" &&
        deletingResource.id === projectId
      ) {
        const next = resources.find(
          (item) => item.kind === "project" && item.id !== deletingResource.id
        )
        if (next) setProjectId(next.id)
      }
      toast.success("配置已删除", { description: deletingResource.name })
      setDeletingResource(null)
    } catch (error) {
      toast.error("删除失败", {
        description: error instanceof Error ? error.message : "请稍后重试",
      })
    }
  }

  return (
    <SidebarProvider>
      <AppSidebar
        section={section}
        onSectionChange={setSection}
        counts={counts}
        projects={projectResources}
        projectId={projectId}
        onProjectChange={setProjectId}
      />
      <SidebarInset className="min-w-0 overflow-hidden">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4 sm:px-6">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-2 h-4" />
          <Breadcrumb>
            <BreadcrumbList>
              <BreadcrumbItem className="hidden sm:block">
                AgentBox Studio
              </BreadcrumbItem>
              <BreadcrumbSeparator className="hidden sm:block" />
              <BreadcrumbItem>
                <BreadcrumbPage>{sectionLabels[section]}</BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
          <div className="ml-auto flex items-center gap-2">
            <div className="hidden items-center gap-2 rounded-full border px-2.5 py-1 text-xs text-muted-foreground sm:flex">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              PostgreSQL
            </div>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="切换主题"
                  onClick={() =>
                    setTheme(
                      document.documentElement.classList.contains("dark")
                        ? "light"
                        : "dark"
                    )
                  }
                >
                  <SunIcon className="hidden dark:block" />
                  <MoonIcon className="dark:hidden" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>切换主题</TooltipContent>
            </Tooltip>
          </div>
        </header>

        <main className="min-h-0 flex-1 overflow-auto">
          {section === "overview" ? (
            <OverviewView
              agents={agents.filter((item) => item.projectId === projectId)}
              resources={resources.filter(
                (item) =>
                  item.kind === "project" || item.projectId === projectId
              )}
              onCreateSandbox={() =>
                setResourceEditor({ kind: "sandbox", resource: null })
              }
            />
          ) : section === "agents" ? (
            <AgentsView
              agents={agents.filter((item) => item.projectId === projectId)}
              catalog={agentCatalog}
              busyId={busyId}
              onCreate={() => setEditor({ open: true, agent: null })}
              onEdit={(agent) => setEditor({ open: true, agent })}
              onDuplicate={duplicate}
              onStatusChange={changeStatus}
              onDelete={setDeleting}
            />
          ) : sectionKinds[section] ? (
            <ResourceView
              kind={sectionKinds[section]!}
              resources={resources}
              projectId={projectId}
              onCreate={() =>
                setResourceEditor({
                  kind: sectionKinds[section]!,
                  resource: null,
                })
              }
              onEdit={(resource) =>
                setResourceEditor({ kind: resource.kind, resource })
              }
              onDelete={setDeletingResource}
            />
          ) : null}
        </main>
      </SidebarInset>

      {editor.open && (
        <AgentEditorDialog
          key={editor.agent?.id ?? "new"}
          agent={editor.agent}
          catalog={agentCatalog}
          onOpenChange={(open) =>
            setEditor({ open, agent: open ? editor.agent : null })
          }
          onSave={save}
        />
      )}

      {resourceEditor && (
        <ResourceEditorDialog
          key={resourceEditor.resource?.id ?? resourceEditor.kind}
          kind={resourceEditor.kind}
          resource={resourceEditor.resource}
          projectId={projectId}
          resources={resources}
          agents={agents.filter((item) => item.projectId === projectId)}
          onOpenChange={(open) => !open && setResourceEditor(null)}
          onSave={saveResource}
        />
      )}

      <AlertDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => !open && setDeleting(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia>
              <Trash2Icon />
            </AlertDialogMedia>
            <AlertDialogTitle>永久删除 {deleting?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              此操作会同时删除该 Agent
              的修订历史，无法恢复。当前版本尚未产生运行记录。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={permanentlyDelete}
              disabled={Boolean(busyId)}
            >
              {busyId ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Trash2Icon data-icon="inline-start" />
              )}
              永久删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={Boolean(deletingResource)}
        onOpenChange={(open) => !open && setDeletingResource(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia>
              <Trash2Icon />
            </AlertDialogMedia>
            <AlertDialogTitle>删除 {deletingResource?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              该声明会从控制面永久删除。已经创建的远端 Sandbox
              不会在这里被强制销毁。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={permanentlyDeleteResource}
            >
              永久删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SidebarProvider>
  )
}

function AgentsView({
  agents,
  catalog,
  busyId,
  onCreate,
  onEdit,
  onDuplicate,
  onStatusChange,
  onDelete,
}: {
  agents: Agent[]
  catalog: Catalog
  busyId: string | null
  onCreate: () => void
  onEdit: (agent: Agent) => void
  onDuplicate: (agent: Agent) => void
  onStatusChange: (agent: Agent, status: AgentStatus) => void
  onDelete: (agent: Agent) => void
}) {
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<"all" | AgentStatus>("all")

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return agents.filter((agent) => {
      const matchesStatus = filter === "all" || agent.status === filter
      const matchesSearch =
        !normalized ||
        [agent.name, agent.slug, agent.description].some((value) =>
          value.toLowerCase().includes(normalized)
        )
      return matchesStatus && matchesSearch
    })
  }, [agents, filter, query])

  const active = agents.filter((agent) => agent.status === "active").length
  const drafts = agents.filter((agent) => agent.status === "draft").length

  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="mb-2 flex items-center gap-2 text-sm text-muted-foreground">
            <CircleDotIcon className="size-3.5 text-emerald-500" /> Agent
            registry
          </div>
          <h1 className="font-heading text-2xl font-semibold tracking-tight sm:text-3xl">
            Agents
          </h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground sm:text-base">
            统一配置角色、模型、Runtime、Skills、MCP、变量与 Sandbox 策略。
          </p>
        </div>
        <Button onClick={onCreate} className="w-full sm:w-auto">
          <PlusIcon data-icon="inline-start" />
          创建 Agent
        </Button>
      </div>

      <div className="mt-6 grid gap-3 sm:grid-cols-3">
        <SummaryCard
          title="全部 Agent"
          value={agents.length}
          description="当前项目中的配置"
          icon={BotIcon}
        />
        <SummaryCard
          title="已启用"
          value={active}
          description="可由 Sandbox 或触发器调用"
          icon={CheckCircle2Icon}
        />
        <SummaryCard
          title="草稿"
          value={drafts}
          description="仍在调整中的配置"
          icon={PencilIcon}
        />
      </div>

      <div className="mt-7 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="relative w-full lg:max-w-sm">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名称、标识或简介…"
            className="pl-9"
          />
        </div>
        <div className="overflow-x-auto pb-1">
          <ToggleGroup
            type="single"
            variant="outline"
            spacing={0}
            value={filter}
            onValueChange={(value) =>
              value && setFilter(value as typeof filter)
            }
          >
            <ToggleGroupItem value="all">全部 {agents.length}</ToggleGroupItem>
            <ToggleGroupItem value="active">已启用 {active}</ToggleGroupItem>
            <ToggleGroupItem value="draft">草稿 {drafts}</ToggleGroupItem>
            <ToggleGroupItem value="archived">
              已归档 {agents.length - active - drafts}
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
      </div>

      {filtered.length > 0 ? (
        <div className="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {filtered.map((agent) => (
            <AgentCard
              key={agent.id}
              agent={agent}
              catalog={catalog}
              busy={busyId === agent.id}
              onEdit={() => onEdit(agent)}
              onDuplicate={() => onDuplicate(agent)}
              onStatusChange={(status) => onStatusChange(agent, status)}
              onDelete={() => onDelete(agent)}
            />
          ))}
        </div>
      ) : (
        <Empty className="mt-4 min-h-80 border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <SearchIcon />
            </EmptyMedia>
            <EmptyTitle>
              {agents.length === 0 ? "还没有 Agent" : "没有匹配的 Agent"}
            </EmptyTitle>
            <EmptyDescription>
              {agents.length === 0
                ? "创建第一个 Agent，声明它的角色、模型和能力。"
                : "尝试清除搜索词或切换状态筛选。"}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            {agents.length === 0 ? (
              <Button onClick={onCreate}>
                <PlusIcon data-icon="inline-start" />
                创建 Agent
              </Button>
            ) : (
              <Button
                variant="outline"
                onClick={() => {
                  setQuery("")
                  setFilter("all")
                }}
              >
                清除筛选
              </Button>
            )}
          </EmptyContent>
        </Empty>
      )}
    </div>
  )
}

function SummaryCard({
  title,
  value,
  description,
  icon: Icon,
}: {
  title: string
  value: number
  description: string
  icon: typeof BotIcon
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardDescription>{title}</CardDescription>
        <CardAction>
          <Icon className="size-4 text-muted-foreground" />
        </CardAction>
      </CardHeader>
      <CardContent>
        <CardTitle className="text-2xl tabular-nums">{value}</CardTitle>
      </CardContent>
      <CardFooter className="text-xs text-muted-foreground">
        {description}
      </CardFooter>
    </Card>
  )
}

function AgentCard({
  agent,
  catalog,
  busy,
  onEdit,
  onDuplicate,
  onStatusChange,
  onDelete,
}: {
  agent: Agent
  catalog: Catalog
  busy: boolean
  onEdit: () => void
  onDuplicate: () => void
  onStatusChange: (status: AgentStatus) => void
  onDelete: () => void
}) {
  const provider = catalog.providers.find(
    (item) => item.id === agent.providerId
  )
  const model = provider?.models.find((item) => item.id === agent.modelId)
  const boundSkills = catalog.skills.filter((item) =>
    agent.skillIds.includes(item.id)
  )

  return (
    <Card
      className={
        agent.status === "archived"
          ? "opacity-75"
          : "transition-shadow hover:shadow-md"
      }
    >
      <CardHeader>
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-foreground text-xs font-semibold text-background">
            {agent.avatar}
          </div>
          <div className="min-w-0">
            <CardTitle className="truncate">{agent.name}</CardTitle>
            <CardDescription className="truncate">
              /{agent.slug}
            </CardDescription>
          </div>
        </div>
        <CardAction>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`${agent.name} 操作`}
                disabled={busy}
              >
                {busy ? <Spinner /> : <MoreHorizontalIcon />}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuLabel>Agent 操作</DropdownMenuLabel>
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={onEdit}>
                  <PencilIcon />
                  编辑配置
                </DropdownMenuItem>
                <DropdownMenuItem onClick={onDuplicate}>
                  <CopyIcon />
                  创建副本
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                {agent.status !== "active" && agent.status !== "archived" && (
                  <DropdownMenuItem onClick={() => onStatusChange("active")}>
                    <CheckCircle2Icon />
                    启用
                  </DropdownMenuItem>
                )}
                {agent.status === "active" && (
                  <DropdownMenuItem onClick={() => onStatusChange("draft")}>
                    <PencilIcon />
                    转为草稿
                  </DropdownMenuItem>
                )}
                {agent.status !== "archived" ? (
                  <DropdownMenuItem onClick={() => onStatusChange("archived")}>
                    <ArchiveIcon />
                    归档
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem onClick={() => onStatusChange("draft")}>
                    <RotateCcwIcon />
                    恢复为草稿
                  </DropdownMenuItem>
                )}
              </DropdownMenuGroup>
              {agent.status === "archived" && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuItem variant="destructive" onClick={onDelete}>
                      <Trash2Icon />
                      永久删除
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </CardAction>
      </CardHeader>

      <CardContent className="grid gap-4">
        <p className="line-clamp-2 min-h-10 text-sm leading-relaxed text-muted-foreground">
          {agent.description || "尚未填写简介。"}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Badge
            variant="outline"
            className={statusMeta[agent.status].className}
          >
            {statusMeta[agent.status].label}
          </Badge>
          <Badge variant="secondary">
            {provider?.name ?? agent.providerId}
          </Badge>
          <Badge variant="secondary">{model?.name ?? agent.modelId}</Badge>
          <Badge variant="outline">{agent.runtimeId}</Badge>
        </div>
        <div className="grid grid-cols-2 gap-3 rounded-lg bg-muted/50 p-3 text-sm">
          <div className="flex items-center gap-2">
            <SparklesIcon className="size-4 text-muted-foreground" />
            <span>{agent.skillIds.length} Skills</span>
          </div>
          <div className="flex items-center gap-2">
            <PlugZapIcon className="size-4 text-muted-foreground" />
            <span>{agent.mcpServerIds.length} MCP</span>
          </div>
        </div>
        {boundSkills.length > 0 && (
          <div className="flex min-w-0 flex-wrap gap-1.5">
            {boundSkills.slice(0, 2).map((skill) => (
              <Badge key={skill.id} variant="outline" className="font-normal">
                {skill.name}
              </Badge>
            ))}
            {boundSkills.length > 2 && (
              <Badge variant="outline" className="font-normal">
                +{boundSkills.length - 2}
              </Badge>
            )}
          </div>
        )}
      </CardContent>

      <CardFooter className="justify-between gap-3 text-xs text-muted-foreground">
        <span>
          v{agent.version} · {updatedLabel(agent.updatedAt)}
        </span>
        <Button variant="ghost" size="xs" onClick={onEdit}>
          查看
          <ChevronRightIcon data-icon="inline-end" />
        </Button>
      </CardFooter>
    </Card>
  )
}
