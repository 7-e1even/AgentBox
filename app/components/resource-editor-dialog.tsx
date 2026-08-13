"use client"

import { useState } from "react"
import { CheckIcon, SaveIcon } from "lucide-react"

import { createSlug, type Agent } from "@/lib/agent-schema"
import type { ManagedCredential } from "@/lib/credential-schema"
import {
  resourceInputSchema,
  type Resource,
  type ResourceInput,
  type ResourceKind,
} from "@/lib/platform-schema"
import type { ManagedServer } from "@/lib/server-schema"
import { Checkbox } from "@/components/ui/checkbox"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

const labels: Record<ResourceKind, string> = {
  project: "项目",
  image: "镜像",
  runtime: "环境模板",
  skill: "Skill",
  mcp: "MCP Server",
  sandbox: "Sandbox",
  schedule: "定时任务",
  webhook: "Webhook",
  variable: "变量引用",
}

type Option = { value: string; label: string }
type SpecField = {
  key: string
  label: string
  placeholder?: string
  description?: string
  textarea?: boolean
  options?: Option[]
  multiOptions?: Option[]
}

function fields(
  kind: ResourceKind,
  agents: Agent[],
  resources: Resource[],
  servers: ManagedServer[],
  credentials: ManagedCredential[],
  spec: Record<string, unknown>
): SpecField[] {
  const agentOptions = agents.map((item) => ({
    value: item.id,
    label: item.name,
  }))
  const runtimeOptions = resources
    .filter((item) => item.kind === "runtime")
    .map((item) => ({ value: item.id, label: item.name }))
  const serverOptions = servers.map((item) => ({
    value: item.id,
    label: `${item.name} · ${item.status === "online" ? "在线" : "离线"}`,
  }))
  const server = servers.find((item) => item.id === spec.serverId)
  const driverOptions = [
    ...(!server || server.capabilities.includes("docker")
      ? [{ value: "docker", label: "Docker 容器" }]
      : []),
    ...(!server || server.capabilities.includes("kvm")
      ? [{ value: "vm", label: "VM 沙箱" }]
      : []),
  ]
  const driver = typeof spec.driver === "string" ? spec.driver : "docker"
  const serverImages =
    driver === "vm"
      ? (server?.inventory.vmImages ?? [])
      : (server?.inventory.dockerImages ?? [])
  const imageOptions = serverImages.map((item) => ({
    value: driver === "vm" ? item.path || item.reference : item.reference,
    label: `${item.reference}${item.size ? ` · ${item.size}` : ""}`,
  }))
  switch (kind) {
    case "project":
      return []
    case "image":
      return [
        {
          key: "reference",
          label: "OCI 镜像引用",
          placeholder: "ubuntu:24.04 或 registry.example.com/agent:latest",
          description:
            "保存镜像引用；实际拉取和 VM rootfs 转换发生在沙箱创建阶段。",
        },
        {
          key: "architecture",
          label: "架构",
          options: [
            { value: "all", label: "多架构 / 自动" },
            { value: "amd64", label: "amd64" },
            { value: "arm64", label: "arm64" },
          ],
        },
        {
          key: "modes",
          label: "兼容类型",
          multiOptions: [
            { value: "docker", label: "Docker 容器" },
            { value: "vm", label: "VM 沙箱" },
          ],
          description: "VM 会将 OCI 镜像转换为可启动的根文件系统。",
        },
      ]
    case "runtime":
      return [
        {
          key: "serverId",
          label: "运行服务器",
          options: serverOptions,
          description: "镜像和虚拟化能力都来自这台物理服务器的实时盘点。",
        },
        {
          key: "driver",
          label: "隔离类型",
          options: driverOptions,
          description:
            driver === "vm"
              ? "需要物理服务器支持 KVM。"
              : "需要物理服务器安装并启用 Docker。",
        },
        {
          key: "imageReference",
          label: "系统镜像",
          options: imageOptions,
          description:
            imageOptions.length > 0
              ? driver === "vm"
                ? "来自服务器 VM 镜像目录，创建时会启动独立虚拟机。"
                : "来自服务器当前已有的 Docker 镜像，不是平台手填目录。"
              : server
                ? "这台服务器尚未上报可用镜像。"
                : "请先选择运行服务器。",
        },
        {
          key: "agentTools",
          label: "预装 Agent 工具",
          multiOptions: [
            { value: "codex", label: "Codex" },
            { value: "claude-code", label: "Claude Code" },
            { value: "gemini-cli", label: "Gemini CLI" },
            { value: "opencode", label: "OpenCode" },
          ],
          description: "创建沙箱时安装并写入所选 Agent 工具的配置。",
        },
        { key: "workdir", label: "工作目录", placeholder: "/workspace" },
        {
          key: "setup",
          label: "初始化命令",
          placeholder: "apt-get update && apt-get install -y git",
          textarea: true,
        },
        { key: "cpu", label: "CPU", placeholder: "2" },
        { key: "memory", label: "内存", placeholder: "4 GiB" },
        {
          key: "network",
          label: "网络策略",
          options: values("none", "restricted", "egress"),
        },
        {
          key: "skillIds",
          label: "预装 Skills",
          multiOptions: resources
            .filter((item) => item.kind === "skill" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
        },
        {
          key: "mcpServerIds",
          label: "预配 MCP Servers",
          multiOptions: resources
            .filter((item) => item.kind === "mcp" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
        },
        {
          key: "variableIds",
          label: "注入环境变量",
          multiOptions: resources
            .filter((item) => item.kind === "variable" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
        },
        {
          key: "credentialIds",
          label: "注入 API Keys",
          multiOptions: credentials
            .filter((item) => item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          description: "只保存凭据标识；明文会在创建沙箱时解密注入。",
        },
      ]
    case "skill":
      return [
        { key: "version", label: "版本", placeholder: "1.0.0" },
        { key: "category", label: "分类", placeholder: "开发" },
        {
          key: "source",
          label: "来源",
          options: values("inline", "git", "local", "builtin"),
        },
        { key: "path", label: "来源路径", placeholder: "skills/code-review" },
        {
          key: "instructions",
          label: "SKILL.md 指令",
          textarea: true,
          placeholder: "说明何时使用以及执行步骤。",
        },
      ]
    case "mcp":
      return [
        {
          key: "transport",
          label: "Transport",
          options: values("stdio", "http"),
        },
        {
          key: "command",
          label: "启动命令",
          placeholder: "npx -y @example/mcp-server",
        },
        { key: "args", label: "参数", placeholder: "--stdio --readonly" },
        {
          key: "url",
          label: "HTTP URL",
          placeholder: "https://mcp.example.com",
        },
        {
          key: "headers",
          label: "Header 引用",
          textarea: true,
          placeholder: "Authorization=secret://MCP_TOKEN",
        },
      ]
    case "sandbox":
      return [
        { key: "agentId", label: "Agent", options: agentOptions },
        { key: "runtimeId", label: "环境模板", options: runtimeOptions },
        {
          key: "serverId",
          label: "目标服务器",
          options: serverOptions,
          description: "沙箱会在这台物理服务器上创建。",
        },
        {
          key: "policy",
          label: "实例策略",
          options: values("new", "reuse", "sticky"),
        },
        {
          key: "workspace",
          label: "工作区目录",
          placeholder: "/workspace/project",
        },
        {
          key: "status",
          label: "期望状态",
          options: values("requested", "running", "stopped"),
        },
      ]
    case "schedule":
      return [
        { key: "agentId", label: "目标 Agent", options: agentOptions },
        {
          key: "cron",
          label: "Cron",
          placeholder: "0 9 * * *",
          description: "标准 5 段 Cron。",
        },
        { key: "timezone", label: "时区", placeholder: "Asia/Shanghai" },
        {
          key: "sandboxPolicy",
          label: "Sandbox 策略",
          options: values("new", "reuse", "sticky"),
        },
        {
          key: "concurrency",
          label: "并发策略",
          options: values("skip", "parallel"),
        },
        {
          key: "prompt",
          label: "触发指令",
          textarea: true,
          placeholder: "检查仓库状态并输出报告。",
        },
      ]
    case "webhook":
      return [
        { key: "agentId", label: "目标 Agent", options: agentOptions },
        {
          key: "path",
          label: "接收路径",
          placeholder: "/hooks/release-review",
        },
        { key: "event", label: "事件类型", placeholder: "push" },
        {
          key: "secretRef",
          label: "签名密钥引用",
          placeholder: "secret://GITHUB_WEBHOOK_SECRET",
        },
        {
          key: "promptTemplate",
          label: "指令模板",
          textarea: true,
          placeholder: "审查 {{event.repository}} 的最新变更。",
        },
      ]
    case "variable":
      return [
        { key: "key", label: "环境变量名", placeholder: "GITHUB_TOKEN" },
        {
          key: "mode",
          label: "解析方式",
          options: values("secret-ref", "value-ref"),
        },
        {
          key: "reference",
          label: "引用",
          placeholder: "env://GITHUB_TOKEN",
          description: "平台只保存引用，不保存明文密钥。",
        },
      ]
  }
}

function values(...items: string[]): Option[] {
  return items.map((value) => ({ value, label: value }))
}

function defaults(kind: ResourceKind) {
  const common: Record<ResourceKind, Record<string, unknown>> = {
    project: {},
    image: {
      reference: "",
      architecture: "all",
      modes: ["docker", "vm"],
    },
    runtime: {
      driver: "docker",
      agentTools: ["codex"],
      workdir: "/workspace",
      cpu: "2",
      memory: "4 GiB",
      network: "restricted",
      skillIds: [],
      mcpServerIds: [],
      variableIds: [],
      credentialIds: [],
    },
    skill: { version: "1.0.0", source: "inline" },
    mcp: { transport: "stdio" },
    sandbox: { policy: "new", status: "requested" },
    schedule: {
      cron: "0 9 * * *",
      timezone: "Asia/Shanghai",
      sandboxPolicy: "new",
      concurrency: "skip",
    },
    webhook: { event: "push" },
    variable: { mode: "secret-ref" },
  }
  return common[kind]
}

function stringArray(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}

function nextSpec(
  kind: ResourceKind,
  spec: Record<string, unknown>,
  key: string,
  value: unknown,
  resources: Resource[],
  servers: ManagedServer[]
) {
  const next = { ...spec, [key]: value }
  if (kind === "sandbox" && key === "runtimeId") {
    const runtime = resources.find(
      (item) => item.kind === "runtime" && item.id === value
    )
    if (runtime?.spec.serverId) next.serverId = runtime.spec.serverId
    return next
  }
  if (kind !== "runtime" || (key !== "driver" && key !== "serverId")) {
    return next
  }

  const server = servers.find((item) => item.id === next.serverId)
  const availableDrivers = [
    ...(!server || server.capabilities.includes("docker") ? ["docker"] : []),
    ...(!server || server.capabilities.includes("kvm") ? ["vm"] : []),
  ]
  if (!availableDrivers.includes(String(next.driver ?? ""))) {
    next.driver = availableDrivers[0] ?? ""
  }

  const images =
    next.driver === "vm"
      ? (server?.inventory.vmImages ?? [])
      : (server?.inventory.dockerImages ?? [])
  const currentReference = String(next.imageReference ?? "")
  const currentExists = images.some((image) =>
    [image.reference, image.path, image.id].includes(currentReference)
  )
  if (!currentExists) {
    const first = images[0]
    next.imageReference =
      first && next.driver === "vm"
        ? first.path || first.reference
        : (first?.reference ?? "")
  }
  return next
}

function initialEditorSpec(
  kind: ResourceKind,
  resources: Resource[],
  servers: ManagedServer[],
  initialSpec?: Record<string, unknown>
) {
  const spec = { ...defaults(kind), ...initialSpec }
  if (kind !== "runtime") return spec

  const selectedServer = servers.find((item) => item.id === spec.serverId)
  const server =
    selectedServer ??
    servers.find((item) => item.status === "online") ??
    servers[0]
  if (server) spec.serverId = server.id
  const selectedDriverSupported =
    spec.driver === "docker"
      ? server?.capabilities.includes("docker")
      : spec.driver === "vm"
        ? server?.capabilities.includes("kvm")
        : false
  if (server && !selectedDriverSupported) {
    spec.driver = server.capabilities.includes("docker")
      ? "docker"
      : server.capabilities.includes("kvm")
        ? "vm"
        : ""
  }
  const legacyImage = resources.find(
    (item) => item.kind === "image" && item.id === spec.imageId
  )
  if (!spec.imageReference && legacyImage) {
    spec.imageReference = legacyImage.spec.reference
  }
  const images =
    spec.driver === "vm"
      ? (server?.inventory.vmImages ?? [])
      : (server?.inventory.dockerImages ?? [])
  const currentReference = String(spec.imageReference ?? "")
  if (
    !images.some((image) =>
      [image.reference, image.path, image.id].includes(currentReference)
    )
  ) {
    const image = images[0]
    spec.imageReference =
      image && spec.driver === "vm"
        ? image.path || image.reference
        : (image?.reference ?? "")
  }
  return spec
}

function specAfterProjectChange(
  kind: ResourceKind,
  spec: Record<string, unknown>
) {
  const next = { ...spec }
  if (kind === "runtime") {
    next.skillIds = []
    next.mcpServerIds = []
    next.variableIds = []
  }
  if (kind === "sandbox") {
    next.agentId = ""
    next.runtimeId = ""
  }
  if (kind === "schedule" || kind === "webhook") {
    next.agentId = ""
  }
  return next
}

export function ResourceEditorDialog({
  kind,
  resource,
  projectId,
  resources,
  agents,
  servers,
  credentials,
  initialSpec,
  onOpenChange,
  onSave,
}: {
  kind: ResourceKind
  resource: Resource | null
  projectId: string
  resources: Resource[]
  agents: Agent[]
  servers: ManagedServer[]
  credentials: ManagedCredential[]
  initialSpec?: Record<string, unknown>
  onOpenChange: (open: boolean) => void
  onSave: (input: ResourceInput) => Promise<void>
}) {
  const [input, setInput] = useState<ResourceInput>(() =>
    resource
      ? resourceInputSchema.parse(resource)
      : {
          id: "",
          kind,
          projectId: kind === "project" || kind === "image" ? null : projectId,
          name: "",
          description: "",
          enabled: true,
          spec: initialEditorSpec(kind, resources, servers, initialSpec),
        }
  )
  const [slugEdited, setSlugEdited] = useState(Boolean(resource))
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const projects = resources.filter((item) => item.kind === "project")
  const projectAgents = agents.filter(
    (item) => item.projectId === input.projectId
  )
  const projectResources = resources.filter(
    (item) =>
      item.kind === "image" ||
      item.kind === "project" ||
      item.projectId === input.projectId
  )

  function update<K extends keyof ResourceInput>(
    key: K,
    value: ResourceInput[K]
  ) {
    setInput((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: "" }))
  }

  function updateSpec(key: string, value: unknown) {
    setInput((current) => ({
      ...current,
      spec: nextSpec(kind, current.spec, key, value, projectResources, servers),
    }))
    setErrors((current) => ({ ...current, spec: "" }))
  }

  function changeProject(nextProjectId: string) {
    setInput((current) => ({
      ...current,
      projectId: nextProjectId,
      spec: specAfterProjectChange(kind, current.spec),
    }))
    setErrors((current) => ({ ...current, projectId: "", spec: "" }))
  }

  async function submit() {
    const parsed = resourceInputSchema.safeParse(input)
    if (!parsed.success) {
      setErrors(
        Object.fromEntries(
          parsed.error.issues.map((issue) => [
            String(issue.path[0]),
            issue.message,
          ])
        )
      )
      return
    }
    if (
      kind === "schedule" &&
      String(input.spec.cron ?? "")
        .trim()
        .split(/\s+/).length !== 5
    ) {
      setErrors({ spec: "Cron 表达式需要 5 段" })
      return
    }
    if (kind === "image" && stringArray(input.spec.modes).length === 0) {
      setErrors({ spec: "请至少选择一种兼容类型" })
      return
    }
    if (kind === "runtime" && !String(input.spec.serverId ?? "").trim()) {
      setErrors({ spec: "请选择运行服务器" })
      return
    }
    if (kind === "runtime" && !String(input.spec.imageReference ?? "").trim()) {
      setErrors({ spec: "请选择服务器上实际存在的系统镜像" })
      return
    }
    if (kind === "sandbox" && !String(input.spec.serverId ?? "").trim()) {
      setErrors({ spec: "请选择创建沙箱的目标服务器" })
      return
    }
    setSaving(true)
    try {
      await onSave(parsed.data)
    } catch (error) {
      setErrors({ form: error instanceof Error ? error.message : "保存失败" })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !saving && onOpenChange(open)}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {resource ? "编辑 " : "创建 "}
            {labels[kind]}
          </DialogTitle>
          <DialogDescription>
            {kind === "project"
              ? "项目只用于组织智能体和相关配置。"
              : kind === "image"
                ? "镜像是平台级 OCI 引用，可供 Docker 与 VM 环境模板复用。"
                : kind === "runtime"
                  ? "先选择运行服务器和它实际拥有的镜像，再配置 Agent 工具与能力。"
                  : "配置会保存在平台控制面，并由沙箱创建流程消费。"}
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          {kind !== "project" && kind !== "image" && (
            <Field>
              <FieldLabel>所属项目</FieldLabel>
              <Select
                value={input.projectId ?? ""}
                onValueChange={changeProject}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择项目" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectLabel>Projects</SelectLabel>
                    {projects.map((item) => (
                      <SelectItem key={item.id} value={item.id}>
                        {item.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.name)}>
              <FieldLabel htmlFor="resource-name">名称</FieldLabel>
              <Input
                id="resource-name"
                autoFocus
                value={input.name}
                aria-invalid={Boolean(errors.name)}
                onChange={(event) => {
                  update("name", event.target.value)
                  if (!slugEdited) update("id", createSlug(event.target.value))
                }}
              />
              <FieldError>{errors.name}</FieldError>
            </Field>
            <Field data-invalid={Boolean(errors.id)}>
              <FieldLabel htmlFor="resource-id">唯一标识</FieldLabel>
              <Input
                id="resource-id"
                value={input.id}
                disabled={Boolean(resource)}
                aria-invalid={Boolean(errors.id)}
                onChange={(event) => {
                  setSlugEdited(true)
                  update("id", event.target.value.toLowerCase())
                }}
              />
              <FieldError>{errors.id}</FieldError>
            </Field>
          </div>
          <Field>
            <FieldLabel htmlFor="resource-description">简介</FieldLabel>
            <Textarea
              id="resource-description"
              value={input.description}
              className="min-h-20"
              onChange={(event) => update("description", event.target.value)}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            {fields(
              kind,
              projectAgents,
              projectResources,
              servers,
              credentials,
              input.spec
            ).map((field) => (
              <Field
                key={field.key}
                className={
                  field.textarea || field.multiOptions
                    ? "sm:col-span-2"
                    : undefined
                }
                data-invalid={Boolean(errors.spec)}
              >
                <FieldLabel htmlFor={`spec-${field.key}`}>
                  {field.label}
                </FieldLabel>
                {field.multiOptions ? (
                  <ToggleGroup
                    type="multiple"
                    variant="selection"
                    className="flex flex-wrap justify-start"
                    value={stringArray(input.spec[field.key])}
                    onValueChange={(value) => updateSpec(field.key, value)}
                  >
                    {field.multiOptions.map((option) => (
                      <ToggleGroupItem key={option.value} value={option.value}>
                        {stringArray(input.spec[field.key]).includes(
                          option.value
                        ) && <CheckIcon data-icon="inline-start" />}
                        {option.label}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                ) : field.options ? (
                  <Select
                    value={
                      kind === "runtime" &&
                      field.key === "serverId" &&
                      !input.spec.serverId
                        ? "__launch__"
                        : String(input.spec[field.key] ?? "")
                    }
                    onValueChange={(value) => updateSpec(field.key, value)}
                  >
                    <SelectTrigger id={`spec-${field.key}`} className="w-full">
                      <SelectValue placeholder="请选择" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>{field.label}</SelectLabel>
                        {field.options.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                ) : field.textarea ? (
                  <Textarea
                    id={`spec-${field.key}`}
                    className="min-h-28 font-mono text-sm"
                    value={String(input.spec[field.key] ?? "")}
                    placeholder={field.placeholder}
                    onChange={(event) =>
                      updateSpec(field.key, event.target.value)
                    }
                  />
                ) : (
                  <Input
                    id={`spec-${field.key}`}
                    value={String(input.spec[field.key] ?? "")}
                    placeholder={field.placeholder}
                    onChange={(event) =>
                      updateSpec(field.key, event.target.value)
                    }
                  />
                )}
                {(field.description || field.multiOptions) && (
                  <FieldDescription aria-live="polite">
                    {field.multiOptions &&
                      `已选择 ${stringArray(input.spec[field.key]).length} 个`}
                    {field.multiOptions && field.description && " · "}
                    {field.description}
                  </FieldDescription>
                )}
              </Field>
            ))}
          </div>
          {kind !== "project" && (
            <Field orientation="horizontal" className="rounded-lg border p-3">
              <Checkbox
                id="resource-enabled"
                checked={input.enabled}
                onCheckedChange={(checked) =>
                  update("enabled", checked === true)
                }
              />
              <div>
                <FieldLabel htmlFor="resource-enabled">启用配置</FieldLabel>
                <FieldDescription>
                  {kind === "image"
                    ? "停用后不能用于创建或更新环境模板。"
                    : "禁用后保留声明，但不会用于新建沙箱。"}
                </FieldDescription>
              </div>
            </Field>
          )}
          {(errors.form || errors.spec) && (
            <FieldError>{errors.form || errors.spec}</FieldError>
          )}
        </FieldGroup>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            取消
          </Button>
          <Button onClick={submit} disabled={saving}>
            {saving ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <SaveIcon data-icon="inline-start" />
            )}
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
