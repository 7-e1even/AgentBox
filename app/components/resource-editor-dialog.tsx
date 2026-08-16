"use client"

import { useState } from "react"
import { CheckIcon, SaveIcon } from "lucide-react"

import { agentToolOptions, supportedAgentToolList } from "@/lib/agent-tools"
import type { ManagedCredential } from "@/lib/credential-schema"
import type { ManagedNetworkProxy } from "@/lib/network-proxy-schema"
import {
  environmentVariablesError,
  sandboxEnvironmentVariables,
} from "@/lib/environment-variables"
import {
  resourceInputSchema,
  type Resource,
  type ResourceInput,
  type ResourceKind,
} from "@/lib/platform-schema"
import {
  normalizeRuntimeImageReference,
  runtimeImageChoices,
  type RuntimeImageChoices,
} from "@/lib/runtime-images"
import type { ManagedServer } from "@/lib/server-schema"
import { EnvironmentVariablesEditor } from "@/components/environment-variables-editor"
import { RuntimeImageCombobox } from "@/components/runtime-image-combobox"
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
  variable: "变量引用",
}

type Option = { value: string; label: string; disabled?: boolean }
type SpecField = {
  key: string
  label: string
  placeholder?: string
  description?: string
  textarea?: boolean
  options?: Option[]
  optionGroupLabel?: string
  imageChoices?: RuntimeImageChoices
  imageDriver?: string
  multiOptions?: Option[]
  environmentVariables?: boolean
  advanced?: boolean
  emptyValue?: string
  disabled?: boolean
}

function runtimeDriverOptions(server?: ManagedServer): Option[] {
  if (!server) return []
  return [
    {
      value: "docker",
      label: server.capabilities.includes("docker")
        ? "Docker 容器"
        : "Docker 容器 · Worker 未就绪",
      disabled: !server.capabilities.includes("docker"),
    },
    {
      value: "boxlite",
      label: server.capabilities.includes("boxlite")
        ? "BoxLite MicroVM（推荐）"
        : "BoxLite MicroVM · SDK 自检未通过",
      disabled: !server.capabilities.includes("boxlite"),
    },
    {
      value: "microsandbox",
      label: server.capabilities.includes("microsandbox")
        ? "Microsandbox（实验）"
        : "Microsandbox · SDK 自检未通过",
      disabled: !server.capabilities.includes("microsandbox"),
    },
  ]
}

function runtimeDriverDescription(server: ManagedServer, driver: string) {
  if (
    server.capabilities.includes("kvm-device") &&
    !server.capabilities.includes("boxlite") &&
    !server.capabilities.includes("microsandbox")
  ) {
    return "服务器已检测到 KVM，但两个 MicroVM SDK 尚未通过 Worker 自检；重新运行 Worker 安装脚本后会自动开放。"
  }
  if (driver === "boxlite") {
    return "由 Worker 通过 BoxLite SDK 创建独立 MicroVM，兼顾硬件隔离和快速启动。"
  }
  if (driver === "microsandbox") {
    return "由 Worker 通过 Microsandbox SDK 创建 MicroVM；当前按实验能力开放。"
  }
  return "由 Worker 通过 Docker 创建容器，适合兼容性优先的环境。"
}

function runtimeImageDescription(driver: string) {
  if (driver === "boxlite") {
    return "可搜索当前服务器的容器镜像；未缓存的 Registry 引用由 BoxLite 在创建时拉取。"
  }
  if (driver === "microsandbox") {
    return "可搜索当前服务器的容器镜像；Microsandbox 会导入可复用内容或从 Registry 拉取。"
  }
  if (driver === "vm") {
    return "只允许选择 Worker 已盘点到的本地 qcow2/raw VM 镜像。"
  }
  return "可搜索当前服务器的容器镜像；未缓存的 Registry 引用会在首次创建时拉取。"
}

function fields(
  kind: ResourceKind,
  resources: Resource[],
  servers: ManagedServer[],
  credentials: ManagedCredential[],
  proxies: ManagedNetworkProxy[],
  spec: Record<string, unknown>
): SpecField[] {
  const runtimeOptions = resources
    .filter((item) => item.kind === "runtime" && item.enabled)
    .map((item) => ({ value: item.id, label: item.name }))
  const serverOptions = servers.map((item) => ({
    value: item.id,
    label: `${item.name} · ${item.status === "online" ? "在线" : "离线"}`,
  }))
  const server = servers.find((item) => item.id === spec.serverId)
  const driverOptions = runtimeDriverOptions(server)
  const driver = typeof spec.driver === "string" ? spec.driver : "docker"
  const imageChoices = runtimeImageChoices(server, driver, spec.imageReference)
  switch (kind) {
    case "project":
      return [
        {
          key: "emoji",
          label: "项目图标",
          placeholder: "📁",
          description:
            "使用系统输入法输入一个 Emoji；留空时使用默认文件夹图标。",
        },
      ]
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
          description: server
            ? runtimeDriverDescription(server, driver)
            : "请先选择运行服务器；只显示已经通过 Worker 自检的驱动。",
        },
        {
          key: "imageReference",
          label: "系统镜像",
          imageChoices,
          imageDriver: driver,
          description: runtimeImageDescription(driver),
        },
        {
          key: "agentTools",
          label: "Agent 工具",
          multiOptions: agentToolOptions,
          description:
            driver === "docker"
              ? "只安装所选工具；首个沙箱会构建缓存镜像，之后同组合直接复用。部分工具仍需各自账号登录。"
              : "只安装所选工具；创建 MicroVM 后在隔离环境内完成安装。部分工具仍需各自账号登录。",
        },
        {
          key: "credentialIds",
          label: "可用模型服务",
          multiOptions: credentials
            .filter((item) => item.enabled)
            .map((item) => ({ value: item.id, label: item.name })),
          description: "模板只限定可使用的模型服务；具体模型在创建沙箱时选择。",
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
          description:
            spec.network === "none"
              ? "完全隔离，不创建环境网络接口。"
              : spec.network === "egress"
                ? "允许环境直接访问出站网络；代理只负责路由兼容。"
                : driver === "boxlite"
                  ? "受限网络配合代理时，只放行代理、直连地址和控制面。"
                  : "Docker 的受限网络依赖环境内程序遵守代理配置。",
          advanced: true,
        },
        {
          key: "proxyId",
          label: "网络代理",
          options: [
            { value: "__none__", label: "不使用代理" },
            ...proxies
              .filter(
                (item) => item.enabled || item.id === String(spec.proxyId ?? "")
              )
              .map((item) => ({
                value: item.id,
                label: `${item.name}${item.enabled ? "" : " · 已停用"}`,
                disabled: !item.enabled,
              })),
          ],
          emptyValue: "__none__",
          disabled: spec.network === "none",
          description:
            spec.network === "none"
              ? "完全隔离时不能使用代理。"
              : "覆盖环境内安装和运行流量；宿主机拉取镜像不受影响。",
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
          key: "environmentVariables",
          label: "环境变量",
          environmentVariables: true,
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
    case "variable":
      return [
        {
          key: "key",
          label: "环境变量名",
          placeholder: "CUSTOM_API_TOKEN",
        },
        {
          key: "mode",
          label: "解析方式",
          options: values("secret-ref", "value-ref"),
        },
        {
          key: "reference",
          label: "引用",
          placeholder: "env://CUSTOM_API_TOKEN",
          description: "平台只保存引用，不保存明文密钥。",
        },
      ]
  }
}

function values(...items: string[]): Option[] {
  return items.map((value) => ({ value, label: value }))
}

function createSlug(value: string) {
  return (
    value
      .normalize("NFKD")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 64) || "resource"
  )
}

function defaults(kind: ResourceKind) {
  const common: Record<ResourceKind, Record<string, unknown>> = {
    project: { emoji: "" },
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
      proxyId: "",
      skillIds: [],
      mcpServerIds: [],
      variableIds: [],
      environmentVariables: sandboxEnvironmentVariables(undefined),
      credentialIds: [],
    },
    skill: { version: "1.0.0", source: "inline" },
    mcp: { transport: "stdio" },
    sandbox: {
      policy: "new",
      status: "requested",
      agentTools: [],
      environmentVariables: sandboxEnvironmentVariables(undefined),
      credentialIds: [],
    },
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
        field.textarea || field.multiOptions || field.environmentVariables
          ? "sm:col-span-2"
          : undefined
      }
      data-invalid={invalid}
    >
      <FieldLabel
        htmlFor={
          field.multiOptions || field.environmentVariables
            ? undefined
            : `spec-${field.key}`
        }
      >
        {field.label}
      </FieldLabel>
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
      ) : field.environmentVariables ? (
        <EnvironmentVariablesEditor
          value={spec[field.key]}
          onChange={(value) => onChange(field.key, value)}
        />
      ) : field.imageChoices ? (
        <RuntimeImageCombobox
          id={`spec-${field.key}`}
          value={String(spec[field.key] ?? "")}
          choices={field.imageChoices}
          driver={field.imageDriver ?? "docker"}
          invalid={invalid}
          onChange={(value) => onChange(field.key, value)}
        />
      ) : field.options ? (
        <Select
          value={
            kind === "runtime" && field.key === "serverId" && !spec.serverId
              ? "__launch__"
              : field.emptyValue && !spec[field.key]
                ? field.emptyValue
                : String(spec[field.key] ?? "")
          }
          disabled={field.disabled}
          onValueChange={(value) =>
            onChange(
              field.key,
              field.emptyValue && value === field.emptyValue ? "" : value
            )
          }
        >
          <SelectTrigger id={`spec-${field.key}`} className="w-full">
            <SelectValue placeholder="请选择" />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectLabel>{field.optionGroupLabel ?? field.label}</SelectLabel>
              {field.options.map((option) => (
                <SelectItem
                  key={option.value}
                  value={option.value}
                  disabled={option.disabled}
                >
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
  if (kind === "runtime" && key === "network" && value === "none") {
    next.proxyId = ""
  }
  if (kind === "sandbox" && key === "runtimeId") {
    const runtime = resources.find(
      (item) => item.kind === "runtime" && item.id === value
    )
    if (runtime) {
      next.serverId = runtime.spec.serverId
      next.agentTools = supportedAgentToolList(runtime.spec.agentTools)
      next.credentialIds = stringArray(runtime.spec.credentialIds)
      next.environmentVariables = sandboxEnvironmentVariables(
        runtime.spec.environmentVariables
      )
    }
    return next
  }
  if (kind !== "runtime" || (key !== "driver" && key !== "serverId")) {
    return next
  }

  const server = servers.find((item) => item.id === next.serverId)
  const availableDrivers = runtimeDriverOptions(server)
    .filter((option) => !option.disabled)
    .map((option) => option.value)
  if (!availableDrivers.includes(String(next.driver ?? ""))) {
    next.driver = availableDrivers[0] ?? ""
  }

  next.imageReference = normalizeRuntimeImageReference(
    server,
    String(next.driver ?? ""),
    next.imageReference
  )
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
      spec.agentTools = supportedAgentToolList(runtime.spec.agentTools)
      spec.credentialIds = stringArray(runtime.spec.credentialIds)
      spec.environmentVariables = sandboxEnvironmentVariables(
        runtime.spec.environmentVariables
      )
    }
    return spec
  }
  if (kind !== "runtime") return spec

  spec.environmentVariables = sandboxEnvironmentVariables(
    spec.environmentVariables
  )

  const selectedServer = servers.find((item) => item.id === spec.serverId)
  const server =
    selectedServer ??
    servers.find((item) => item.status === "online") ??
    servers[0]
  if (server) spec.serverId = server.id
  const selectedDriverSupported = runtimeDriverOptions(server).some(
    (option) => option.value === spec.driver && !option.disabled
  )
  if (server && !selectedDriverSupported) {
    spec.driver =
      runtimeDriverOptions(server).find((option) => !option.disabled)?.value ??
      ""
  }
  const legacyImage = resources.find(
    (item) => item.kind === "image" && item.id === spec.imageId
  )
  if (!spec.imageReference && legacyImage) {
    spec.imageReference = legacyImage.spec.reference
  }
  spec.imageReference = normalizeRuntimeImageReference(
    server,
    String(spec.driver ?? ""),
    spec.imageReference
  )
  return spec
}

function inputFromResource(resource: Resource): ResourceInput {
  const input = resourceInputSchema.parse(resource)
  if (resource.kind !== "runtime" && resource.kind !== "sandbox") return input
  return {
    ...input,
    spec: {
      ...input.spec,
      agentTools: supportedAgentToolList(input.spec.agentTools),
    },
  }
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
  return next
}

export function ResourceEditorDialog({
  kind,
  resource,
  projectId,
  resources,
  servers,
  credentials,
  proxies,
  initialSpec,
  onOpenChange,
  onSave,
}: {
  kind: ResourceKind
  resource: Resource | null
  projectId: string
  resources: Resource[]
  servers: ManagedServer[]
  credentials: ManagedCredential[]
  proxies: ManagedNetworkProxy[]
  initialSpec?: Record<string, unknown>
  onOpenChange: (open: boolean) => void
  onSave: (input: ResourceInput) => Promise<void>
}) {
  const [input, setInput] = useState<ResourceInput>(() =>
    resource
      ? inputFromResource(resource)
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
  const projectResources = resources.filter(
    (item) =>
      item.kind === "image" ||
      item.kind === "project" ||
      item.projectId === input.projectId
  )
  const specFields = fields(
    kind,
    projectResources,
    servers,
    credentials,
    proxies,
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
    if (kind === "image" && stringArray(input.spec.modes).length === 0) {
      setErrors({ spec: "请至少选择一种兼容类型" })
      return
    }
    if (kind === "runtime" && !String(input.spec.serverId ?? "").trim()) {
      setErrors({ spec: "请选择运行服务器" })
      return
    }
    if (kind === "runtime" && !String(input.spec.imageReference ?? "").trim()) {
      setErrors({
        spec:
          input.spec.driver === "vm"
            ? "请选择 Worker 本地存在的 VM 镜像"
            : "请填写可用的 OCI 镜像引用",
      })
      return
    }
    if (
      kind === "runtime" &&
      input.spec.network === "none" &&
      String(input.spec.proxyId ?? "").trim()
    ) {
      setErrors({ spec: "完全隔离网络不能同时使用代理" })
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
    const environmentError = environmentVariablesError(
      input.spec.environmentVariables
    )
    if (environmentError) {
      setErrors({ spec: environmentError })
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
        onEscapeKeyDown={(event) => {
          if (
            document.querySelector('[data-slot="combobox-content"][data-open]')
          ) {
            event.preventDefault()
          }
        }}
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
