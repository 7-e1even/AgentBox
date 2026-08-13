"use client"

import {
  BotIcon,
  BoxIcon,
  BoxesIcon,
  Clock3Icon,
  Code2Icon,
  ContainerIcon,
  FolderGit2Icon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlugZapIcon,
  PlusIcon,
  SparklesIcon,
  Trash2Icon,
  WebhookIcon,
} from "lucide-react"

import type { Agent } from "@/lib/agent-schema"
import type { Resource, ResourceKind } from "@/lib/platform-schema"
import { Badge } from "@/components/ui/badge"
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

const meta = {
  project: {
    title: "Projects",
    singular: "Project",
    description:
      "定义仓库、本地目录与环境边界。一个 Project 可以拥有多个 Agent 和 Sandbox。",
    icon: FolderGit2Icon,
  },
  runtime: {
    title: "Runtimes",
    singular: "Runtime",
    description:
      "声明 Docker、主机虚拟环境或微虚机模板；实际创建由 Runtime worker 执行。",
    icon: ContainerIcon,
  },
  skill: {
    title: "Skills",
    singular: "Skill",
    description: "管理可安装到 Agent 沙箱中的指令、脚本和资源。",
    icon: SparklesIcon,
  },
  mcp: {
    title: "MCP Servers",
    singular: "MCP Server",
    description: "配置 stdio 或 HTTP 工具服务，并按 Agent 绑定。",
    icon: PlugZapIcon,
  },
  sandbox: {
    title: "Sandboxes",
    singular: "Sandbox",
    description: "查看和声明 Agent 的隔离运行实例；实例不会形成工作流。",
    icon: BoxIcon,
  },
  schedule: {
    title: "Schedules",
    singular: "Schedule",
    description: "按 Cron 直接触发一个 Agent，并选择新建或复用 Sandbox。",
    icon: Clock3Icon,
  },
  webhook: {
    title: "Webhooks",
    singular: "Webhook",
    description: "把外部事件映射成一次 Agent 运行，不串联其他任务节点。",
    icon: WebhookIcon,
  },
  variable: {
    title: "Variables & Secrets",
    singular: "Variable",
    description:
      "只保存环境变量和密钥引用，明文由 Runtime worker 在宿主机解析。",
    icon: KeyRoundIcon,
  },
} satisfies Record<
  ResourceKind,
  { title: string; singular: string; description: string; icon: typeof BotIcon }
>

function text(spec: Record<string, unknown>, key: string) {
  const value = spec[key]
  return typeof value === "string" && value ? value : "—"
}

function highlights(resource: Resource) {
  switch (resource.kind) {
    case "project":
      return [
        ["来源", text(resource.spec, "source")],
        ["仓库", text(resource.spec, "repository")],
        ["分支", text(resource.spec, "branch")],
      ]
    case "runtime":
      return [
        ["驱动", text(resource.spec, "driver")],
        [
          "基础",
          text(resource.spec, "image") !== "—"
            ? text(resource.spec, "image")
            : text(resource.spec, "base"),
        ],
        [
          "资源",
          `${text(resource.spec, "cpu")} CPU · ${text(resource.spec, "memory")}`,
        ],
      ]
    case "skill":
      return [
        ["版本", text(resource.spec, "version")],
        ["来源", text(resource.spec, "source")],
        ["路径", text(resource.spec, "path")],
      ]
    case "mcp":
      return [
        ["Transport", text(resource.spec, "transport")],
        ["命令", text(resource.spec, "command")],
        ["URL", text(resource.spec, "url")],
      ]
    case "sandbox":
      return [
        ["Agent", text(resource.spec, "agentId")],
        ["Runtime", text(resource.spec, "runtimeId")],
        ["状态", text(resource.spec, "status")],
      ]
    case "schedule":
      return [
        ["Cron", text(resource.spec, "cron")],
        ["时区", text(resource.spec, "timezone")],
        ["Sandbox", text(resource.spec, "sandboxPolicy")],
      ]
    case "webhook":
      return [
        ["事件", text(resource.spec, "event")],
        ["路径", text(resource.spec, "path")],
        ["密钥", text(resource.spec, "secretRef")],
      ]
    case "variable":
      return [
        ["Key", text(resource.spec, "key")],
        ["模式", text(resource.spec, "mode")],
        ["引用", text(resource.spec, "reference")],
      ]
  }
}

export function OverviewView({
  agents,
  resources,
  onCreateSandbox,
}: {
  agents: Agent[]
  resources: Resource[]
  onCreateSandbox: () => void
}) {
  const count = (kind: ResourceKind) =>
    resources.filter((item) => item.kind === kind).length
  const triggers = resources.filter(
    (item) =>
      (item.kind === "schedule" || item.kind === "webhook") && item.enabled
  ).length
  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <Badge variant="outline" className="mb-3">
            <span className="size-1.5 rounded-full bg-emerald-500" /> Control
            plane online
          </Badge>
          <h1 className="font-heading text-2xl font-semibold tracking-tight sm:text-3xl">
            Agent 沙箱控制台
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">
            在平台声明 Agent、运行环境和能力，随后创建隔离
            Sandbox。这里负责生命周期，不做任务编排。
          </p>
        </div>
        <Button onClick={onCreateSandbox}>
          <PlusIcon data-icon="inline-start" />
          创建 Sandbox
        </Button>
      </div>
      <div className="mt-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat
          title="Agents"
          value={agents.length}
          hint={`${agents.filter((item) => item.status === "active").length} 个已启用`}
          icon={BotIcon}
        />
        <Stat
          title="Runtime templates"
          value={count("runtime")}
          hint="容器与虚拟环境"
          icon={ContainerIcon}
        />
        <Stat
          title="Sandboxes"
          value={count("sandbox")}
          hint="实例与期望状态"
          icon={BoxesIcon}
        />
        <Stat
          title="Active triggers"
          value={triggers}
          hint="Schedules + Webhooks"
          icon={WebhookIcon}
        />
      </div>
      <div className="mt-6 grid gap-4 lg:grid-cols-[minmax(0,1.4fr)_minmax(20rem,.6fr)]">
        <Card>
          <CardHeader>
            <CardTitle>从声明到运行</CardTitle>
            <CardDescription>
              Runtime worker 只执行控制面生成的单次运行请求。
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 sm:grid-cols-4">
            {[
              ["1", "Project", "仓库与工作区"],
              ["2", "Agent", "模型、Skills、MCP"],
              ["3", "Runtime", "驱动与资源策略"],
              ["4", "Sandbox", "隔离运行实例"],
            ].map(([step, title, description]) => (
              <div key={step} className="rounded-xl border bg-muted/30 p-4">
                <span className="flex size-6 items-center justify-center rounded-full bg-foreground text-xs text-background">
                  {step}
                </span>
                <p className="mt-4 font-medium">{title}</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  {description}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>能力与触发</CardTitle>
            <CardDescription>
              能力是声明，触发器只产生一次 Agent 运行。
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 text-sm">
            <Row icon={SparklesIcon} label="Skills" value={count("skill")} />
            <Row icon={PlugZapIcon} label="MCP Servers" value={count("mcp")} />
            <Row
              icon={Clock3Icon}
              label="Schedules"
              value={count("schedule")}
            />
            <Row icon={WebhookIcon} label="Webhooks" value={count("webhook")} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function Stat({
  title,
  value,
  hint,
  icon: Icon,
}: {
  title: string
  value: number
  hint: string
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
      <CardFooter className="text-xs text-muted-foreground">{hint}</CardFooter>
    </Card>
  )
}

function Row({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof BotIcon
  label: string
  value: number
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border p-3">
      <Icon className="size-4 text-muted-foreground" />
      <span>{label}</span>
      <Badge variant="secondary" className="ml-auto">
        {value}
      </Badge>
    </div>
  )
}

export function ResourceView({
  kind,
  resources,
  projectId,
  onCreate,
  onEdit,
  onDelete,
}: {
  kind: ResourceKind
  resources: Resource[]
  projectId: string
  onCreate: () => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}) {
  const current = resources.filter(
    (item) =>
      item.kind === kind && (kind === "project" || item.projectId === projectId)
  )
  const config = meta[kind]
  const Icon = config.icon
  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex items-start gap-3">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary text-primary-foreground">
            <Icon />
          </div>
          <div>
            <h1 className="font-heading text-2xl font-semibold tracking-tight sm:text-3xl">
              {config.title}
            </h1>
            <p className="mt-1 max-w-2xl text-sm text-muted-foreground sm:text-base">
              {config.description}
            </p>
          </div>
        </div>
        <Button onClick={onCreate}>
          <PlusIcon data-icon="inline-start" />
          创建 {config.singular}
        </Button>
      </div>
      {current.length ? (
        <div className="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {current.map((resource) => (
            <ResourceCard
              key={resource.id}
              resource={resource}
              onEdit={() => onEdit(resource)}
              onDelete={() => onDelete(resource)}
            />
          ))}
        </div>
      ) : (
        <Empty className="mt-6 min-h-80 border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Icon />
            </EmptyMedia>
            <EmptyTitle>还没有 {config.singular}</EmptyTitle>
            <EmptyDescription>{config.description}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={onCreate}>
              <PlusIcon data-icon="inline-start" />
              创建第一个
            </Button>
          </EmptyContent>
        </Empty>
      )}
    </div>
  )
}

function ResourceCard({
  resource,
  onEdit,
  onDelete,
}: {
  resource: Resource
  onEdit: () => void
  onDelete: () => void
}) {
  const Icon = meta[resource.kind].icon
  return (
    <Card className="transition-shadow hover:shadow-md">
      <CardHeader>
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
            <Icon />
          </div>
          <div className="min-w-0">
            <CardTitle className="truncate">{resource.name}</CardTitle>
            <CardDescription className="truncate">
              /{resource.id}
            </CardDescription>
          </div>
        </div>
        <CardAction>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`${resource.name} 操作`}
              >
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>配置操作</DropdownMenuLabel>
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={onEdit}>
                  <PencilIcon />
                  编辑
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem variant="destructive" onClick={onDelete}>
                  <Trash2Icon />
                  删除
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </CardAction>
      </CardHeader>
      <CardContent className="grid gap-4">
        <p className="line-clamp-2 min-h-10 text-sm leading-relaxed text-muted-foreground">
          {resource.description || "尚未填写简介。"}
        </p>
        <div className="grid grid-cols-3 gap-2 rounded-lg bg-muted/40 p-3">
          {highlights(resource).map(([label, value]) => (
            <div key={label} className="min-w-0">
              <p className="text-[11px] text-muted-foreground">{label}</p>
              <p className="truncate text-xs font-medium" title={value}>
                {value}
              </p>
            </div>
          ))}
        </div>
      </CardContent>
      <CardFooter className="justify-between">
        <Badge variant={resource.enabled ? "secondary" : "outline"}>
          {resource.enabled ? "已启用" : "已禁用"}
        </Badge>
        <Button variant="ghost" size="xs" onClick={onEdit}>
          配置
          <Code2Icon data-icon="inline-end" />
        </Button>
      </CardFooter>
    </Card>
  )
}
