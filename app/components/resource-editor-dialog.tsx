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
  runtime: "沙箱模板",
  skill: "Skill",
  mcp: "MCP Server",
  sandbox: "Sandbox",
  schedule: "定时任务",
  webhook: "Webhook",
  variable: "变量引用",
}

type Option = { value: string; label: string; disabled?: boolean }
type Preset = { label: string; values: string[] }
type SpecField = {
  key: string
  label: string
  placeholder?: string
  description?: string
  textarea?: boolean
  options?: Option[]
  multiOptions?: Option[]
  presets?: Preset[]
  advanced?: boolean
}

const commonAgentToolIds = [
  "codex",
  "claude-code",
  "gemini-cli",
  "opencode",
  "pi",
  "copilot-cli",
  "qwen-code",
]

const multicaLinuxAgentToolIds = [
  "antigravity",
  "claude-code",
  "codebuddy",
  "codex",
  "copilot-cli",
  "cursor",
  "grok",
  "kimi",
  "omp",
  "openclaw",
  "opencode",
  "pi",
  "qoder-cli",
  "qoder-cn",
  "qwen-code",
  "qwenpaw",
  "reasonix",
]

const agentToolOptions: Option[] = [
  { value: "antigravity", label: "Antigravity" },
  { value: "claude-code", label: "Claude Code" },
  { value: "codebuddy", label: "CodeBuddy" },
  { value: "codex", label: "Codex" },
  { value: "copilot-cli", label: "GitHub Copilot CLI" },
  { value: "cursor", label: "Cursor CLI" },
  { value: "deveco", label: "DevEco Code（Linux 不支持）", disabled: true },
  { value: "gemini-cli", label: "Gemini CLI" },
  { value: "grok", label: "Grok CLI" },
  { value: "kimi", label: "Kimi Code CLI" },
  { value: "omp", label: "Oh-My-Pi" },
  { value: "openclaw", label: "OpenClaw" },
  { value: "opencode", label: "OpenCode" },
  { value: "pi", label: "Pi" },
  { value: "qoder-cli", label: "Qoder CLI" },
  { value: "qoder-cn", label: "Qoder CLI CN" },
  { value: "qwen-code", label: "Qwen Code" },
  { value: "qwenpaw", label: "QwenPaw" },
  { value: "reasonix", label: "Reasonix" },
  { value: "trae-cli", label: "TRAE CLI（需自带）", disabled: true },
]

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
    .filter((item) => item.kind === "runtime" && item.enabled)
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
  const inventoryImageOptions = serverImages.map((item) => ({
    value: driver === "vm" ? item.path || item.reference : item.reference,
    label: `${item.reference}${item.size ? ` · ${item.size}` : ""}`,
  }))
  const currentImageReference = String(spec.imageReference ?? "").trim()
  const imageOptions =
    driver === "vm"
      ? inventoryImageOptions
      : [
          ...inventoryImageOptions,
          ...(!currentImageReference ||
          inventoryImageOptions.some(
            (option) => option.value === currentImageReference
          )
            ? []
            : [
                {
                  value: currentImageReference,
                  label: `${currentImageReference} · 创建时自动拉取`,
                },
              ]),
          ...(inventoryImageOptions.some(
            (option) => option.value === "ubuntu:24.04"
          ) || currentImageReference === "ubuntu:24.04"
            ? []
            : [
                {
                  value: "ubuntu:24.04",
                  label: "ubuntu:24.04 · 创建时自动拉取",
                },
              ]),
        ]
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
          description: "基座会使用这台服务器的镜像与隔离能力。",
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
            driver === "vm"
              ? imageOptions.length > 0
                ? "来自服务器 VM 镜像目录，创建时会启动独立虚拟机。"
                : server
                  ? "这台服务器尚未上报可用的 VM 镜像。"
                  : "请先选择运行服务器。"
              : "服务器没有该 Docker 镜像时会在首次创建沙箱时自动拉取。",
        },
        {
          key: "agentTools",
          label: "Agent 工具",
          multiOptions: agentToolOptions,
          presets: [
            { label: "常用 7 个", values: commonAgentToolIds },
            {
              label: "全部 17 个（重型）",
              values: multicaLinuxAgentToolIds,
            },
            { label: "清空", values: [] },
          ],
          description:
            "只安装所选工具；首个沙箱会构建缓存镜像，之后同组合直接复用。部分工具仍需各自账号登录。",
        },
        {
          key: "credentialIds",
          label: "模型凭据",
          multiOptions: credentials
            .filter((item) => item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          description: "创建沙箱时自动转换为各 Agent 工具需要的配置格式。",
        },
        {
          key: "workdir",
          label: "工作目录",
          placeholder: "/workspace",
          advanced: true,
        },
        {
          key: "setup",
          label: "初始化命令",
          placeholder: "apt-get update && apt-get install -y git",
          textarea: true,
          advanced: true,
        },
        { key: "cpu", label: "CPU", placeholder: "2", advanced: true },
        {
          key: "memory",
          label: "内存",
          placeholder: "4 GiB",
          advanced: true,
        },
        {
          key: "network",
          label: "网络策略",
          options: values("none", "restricted", "egress"),
          advanced: true,
        },
        {
          key: "skillIds",
          label: "预装 Skills",
          multiOptions: resources
            .filter((item) => item.kind === "skill" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          advanced: true,
        },
        {
          key: "mcpServerIds",
          label: "预配 MCP Servers",
          multiOptions: resources
            .filter((item) => item.kind === "mcp" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          advanced: true,
        },
        {
          key: "variableIds",
          label: "注入环境变量",
          multiOptions: resources
            .filter((item) => item.kind === "variable" && item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          advanced: true,
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
        {
          key: "runtimeId",
          label: "沙箱模板",
          options: runtimeOptions,
          description: "先继承基座配置，再按这个沙箱的需要调整。",
        },
        {
          key: "workspace",
          label: "工作区挂载点（可选）",
          placeholder: "/workspace/project",
        },
        {
          key: "agentTools",
          label: "沙箱内的 Agent（可多选）",
          multiOptions: agentToolOptions,
          presets: [
            { label: "常用 7 个", values: commonAgentToolIds },
            { label: "全部", values: multicaLinuxAgentToolIds },
            { label: "清空", values: [] },
          ],
          description: "同一个沙箱可以同时安装并运行多个 Agent CLI。",
        },
        {
          key: "credentialIds",
          label: "模型凭据",
          multiOptions: credentials
            .filter((item) => item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          description: "为所选 Agent 注入需要的模型访问配置。",
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
      imageReference: "ubuntu:24.04",
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
    sandbox: {
      policy: "new",
      status: "requested",
      agentTools: [],
      credentialIds: [],
    },
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

function SpecFieldEditor({
  field,
  kind,
  spec,
  invalid,
  onChange,
}: {
  field: SpecField
  kind: ResourceKind
  spec: Record<string, unknown>
  invalid: boolean
  onChange: (key: string, value: unknown) => void
}) {
  return (
    <Field
      className={
        field.textarea || field.multiOptions ? "sm:col-span-2" : undefined
      }
      data-invalid={invalid}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <FieldLabel htmlFor={`spec-${field.key}`}>{field.label}</FieldLabel>
        {field.presets && (
          <div className="flex flex-wrap items-center gap-1.5">
            {field.presets.map((preset) => (
              <Button
                key={preset.label}
                type="button"
                variant="outline"
                size="xs"
                onClick={() => onChange(field.key, preset.values)}
              >
                {preset.label}
              </Button>
            ))}
          </div>
        )}
      </div>
      {field.multiOptions ? (
        <ToggleGroup
          type="multiple"
          variant="selection"
          size="sm"
          className="flex flex-wrap justify-start"
          value={stringArray(spec[field.key])}
          onValueChange={(value) => onChange(field.key, value)}
        >
          {field.multiOptions.map((option) => (
            <ToggleGroupItem
              key={option.value}
              value={option.value}
              disabled={option.disabled}
            >
              {stringArray(spec[field.key]).includes(option.value) && (
                <CheckIcon data-icon="inline-start" />
              )}
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      ) : field.options ? (
        <Select
          value={
            kind === "runtime" && field.key === "serverId" && !spec.serverId
              ? "__launch__"
              : String(spec[field.key] ?? "")
          }
          onValueChange={(value) => onChange(field.key, value)}
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
          value={String(spec[field.key] ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onChange(field.key, event.target.value)}
        />
      ) : (
        <Input
          id={`spec-${field.key}`}
          value={String(spec[field.key] ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onChange(field.key, event.target.value)}
        />
      )}
      {(field.description || field.multiOptions) && (
        <FieldDescription aria-live="polite">
          {field.multiOptions &&
            `已选择 ${stringArray(spec[field.key]).length} 个`}
          {field.multiOptions && field.description && " · "}
          {field.description}
        </FieldDescription>
      )}
    </Field>
  )
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
    if (runtime) {
      next.serverId = runtime.spec.serverId
      next.agentTools = stringArray(runtime.spec.agentTools)
      next.credentialIds = stringArray(runtime.spec.credentialIds)
    }
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
  const currentExists =
    (next.driver === "docker" &&
      key !== "driver" &&
      Boolean(currentReference.trim())) ||
    images.some((image) =>
      [image.reference, image.path, image.id].includes(currentReference)
    )
  if (!currentExists) {
    const first = images[0]
    next.imageReference =
      first && next.driver === "vm"
        ? first.path || first.reference
        : (first?.reference ?? "ubuntu:24.04")
  }
  return next
}

function initialEditorSpec(
  kind: ResourceKind,
  resources: Resource[],
  servers: ManagedServer[],
  projectId: string,
  initialSpec?: Record<string, unknown>
) {
  const spec = { ...defaults(kind), ...initialSpec }
  if (kind === "sandbox") {
    const runtime =
      resources.find(
        (item) =>
          item.kind === "runtime" &&
          item.enabled &&
          item.projectId === projectId &&
          item.id === spec.runtimeId
      ) ??
      resources.find(
        (item) =>
          item.kind === "runtime" &&
          item.enabled &&
          item.projectId === projectId
      )
    if (runtime) {
      spec.runtimeId = runtime.id
      spec.serverId = runtime.spec.serverId
      spec.agentTools = stringArray(runtime.spec.agentTools)
      spec.credentialIds = stringArray(runtime.spec.credentialIds)
    }
    return spec
  }
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
    spec.driver !== "docker" &&
    !images.some((image) =>
      [image.reference, image.path, image.id].includes(currentReference)
    )
  ) {
    const image = images[0]
    spec.imageReference =
      image && spec.driver === "vm"
        ? image.path || image.reference
        : (image?.reference ?? "ubuntu:24.04")
  }
  if (spec.driver === "docker" && !currentReference.trim()) {
    spec.imageReference = images[0]?.reference ?? "ubuntu:24.04"
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
    next.runtimeId = ""
    next.serverId = ""
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
          spec: initialEditorSpec(
            kind,
            resources,
            servers,
            projectId,
            initialSpec
          ),
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
  const specFields = fields(
    kind,
    projectAgents,
    projectResources,
    servers,
    credentials,
    input.spec
  )
  const primarySpecFields = specFields.filter((field) => !field.advanced)
  const advancedSpecFields = specFields.filter((field) => field.advanced)

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
    if (kind === "sandbox" && !String(input.spec.runtimeId ?? "").trim()) {
      setErrors({ spec: "请先创建并选择一个沙箱模板" })
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
      <DialogContent
        className={`max-h-[92svh] overflow-y-auto ${
          kind === "sandbox" ? "sm:max-w-xl" : "sm:max-w-4xl"
        }`}
      >
        <DialogHeader>
          <DialogTitle>
            {resource ? "编辑 " : "创建 "}
            {labels[kind]}
          </DialogTitle>
          <DialogDescription>
            {kind === "project"
              ? "项目只用于组织智能体和相关配置。"
              : kind === "image"
                ? "镜像是平台级 OCI 引用，可供 Docker 与 VM 沙箱模板复用。"
                : kind === "runtime"
                  ? "把服务器、镜像、Agent 工具与模型凭据保存成可复用基座。"
                  : kind === "sandbox"
                    ? "从基座继承默认配置，并为这个沙箱选择一个或多个 Agent。"
                    : "配置会保存在平台控制面，并由沙箱创建流程消费。"}
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          {kind !== "project" &&
            kind !== "image" &&
            kind !== "runtime" &&
            kind !== "sandbox" && (
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
          {kind !== "sandbox" && (
            <Field>
              <FieldLabel htmlFor="resource-description">简介</FieldLabel>
              <Textarea
                id="resource-description"
                value={input.description}
                className="min-h-20"
                onChange={(event) => update("description", event.target.value)}
              />
            </Field>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            {primarySpecFields.map((field) => (
              <SpecFieldEditor
                key={field.key}
                field={field}
                kind={kind}
                spec={input.spec}
                invalid={Boolean(errors.spec)}
                onChange={updateSpec}
              />
            ))}
          </div>
          {advancedSpecFields.length > 0 && (
            <details className="group rounded-xl border">
              <summary className="cursor-pointer list-none px-4 py-3 text-sm font-medium">
                高级配置
                <span className="ml-2 font-normal text-muted-foreground">
                  工作目录、资源、网络与扩展能力
                </span>
              </summary>
              <div className="grid gap-4 border-t p-4 sm:grid-cols-2">
                {advancedSpecFields.map((field) => (
                  <SpecFieldEditor
                    key={field.key}
                    field={field}
                    kind={kind}
                    spec={input.spec}
                    invalid={Boolean(errors.spec)}
                    onChange={updateSpec}
                  />
                ))}
              </div>
            </details>
          )}
          {kind !== "project" && kind !== "sandbox" && (
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
