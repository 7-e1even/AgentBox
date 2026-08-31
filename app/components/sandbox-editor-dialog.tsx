"use client"

import { useMemo, useState, type ReactNode } from "react"
import {
  BoxIcon,
  CheckIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  ServerIcon,
} from "lucide-react"

import {
  agentToolOptions,
  incompatibleAgentTools,
  supportedAgentToolList,
} from "@/lib/agent-tools"
import type { ManagedCredential } from "@/lib/credential-schema"
import type { ManagedNetworkProxy } from "@/lib/network-proxy-schema"
import {
  environmentVariablesError,
  sandboxEnvironmentVariables,
} from "@/lib/environment-variables"
import { reconcileModelBindings } from "@/lib/model-bindings"
import {
  resourceInputSchema,
  sandboxSpecForUpdate,
  type Resource,
  type ResourceDraft,
  type ResourceInput,
  type ResourceOfKind,
} from "@/lib/platform-schema"
import {
  normalizeRuntimeImageReference,
  runtimeImageChoices,
} from "@/lib/runtime-images"
import type { ManagedServer } from "@/lib/server-schema"
import { EnvironmentVariablesEditor } from "@/components/environment-variables-editor"
import { RuntimeImageCombobox } from "@/components/runtime-image-combobox"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
  FieldLegend,
  FieldSet,
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
import { Separator } from "@/components/ui/separator"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type SandboxEditorDialogProps = {
  resource: Resource | null
  projectId: string
  resources: Resource[]
  servers: ManagedServer[]
  credentials: ManagedCredential[]
  proxies: ManagedNetworkProxy[]
  initialRuntimeId?: string
  dependenciesReady?: boolean
  dependencyStatus?: ReactNode
  onOpenChange: (open: boolean) => void
  onSave: (input: ResourceInput) => Promise<void>
}

export function SandboxEditorDialog({
  resource,
  projectId,
  resources,
  servers,
  credentials,
  proxies,
  initialRuntimeId,
  dependenciesReady = true,
  dependencyStatus,
  onOpenChange,
  onSave,
}: SandboxEditorDialogProps) {
  const templates = useMemo(
    () =>
      resources
        .filter((item) => item.kind === "runtime")
        .filter((item) => item.projectId === projectId && item.enabled),
    [projectId, resources]
  )
  const initialTemplate =
    templates.find(
      (item) =>
        item.id ===
        (resource?.kind === "sandbox"
          ? resource.spec.runtimeId
          : initialRuntimeId)
    ) ??
    templates.find(
      (item) => item.spec.driver === "docker" && isTemplateReady(item, servers)
    ) ??
    templates.find((item) => isTemplateReady(item, servers)) ??
    templates.find((item) => item.spec.driver === "docker") ??
    templates[0]
  const [input, setInput] = useState<ResourceDraft>(() =>
    resource
      ? sandboxInputFromResource(resource, initialTemplate, credentials)
      : createSandboxInput(projectId, resources, initialTemplate, credentials)
  )
  const [error, setError] = useState("")
  const [saving, setSaving] = useState(false)
  const template = templates.find((item) => item.id === input.spec.runtimeId)
  const server = servers.find((item) => item.id === input.spec.serverId)
  const driver = stringValue(input.spec.driver)
  const requiredCapability = driver === "vm" ? "kvm" : driver
  const configurationReady = Boolean(
    template &&
    server?.status === "online" &&
    server.capabilities.includes(requiredCapability) &&
    stringValue(input.spec.imageReference)
  )
  const credentialIds = stringList(input.spec.credentialIds)
  const modelBindings = stringRecord(input.spec.modelBindings)
  const selectedCredentials = credentialIds.map((id) => ({
    id,
    credential: credentials.find((item) => item.id === id && item.enabled),
  }))
  const enabledCredentials = credentials.filter((item) => item.enabled)
  const projectSkills = projectResources(resources, projectId, "skill")
  const projectMCPServers = projectResources(resources, projectId, "mcp")
  const driverOptions = runtimeDriverOptions(server)
  const imageChoices = runtimeImageChoices(
    server,
    driver,
    input.spec.imageReference
  )
  const lockedTools = useMemo(
    () =>
      resource?.kind === "sandbox"
        ? supportedAgentToolList(resource.spec.agentTools)
        : [],
    [resource]
  )

  function update<K extends keyof ResourceDraft>(
    key: K,
    value: ResourceDraft[K]
  ) {
    setInput((current) => ({ ...current, [key]: value }))
    setError("")
  }

  function updateSpec(key: string, value: unknown) {
    setInput((current) => ({
      ...current,
      spec: {
        ...current.spec,
        [key]: value,
        ...(key === "network" && value === "none" ? { proxyId: "" } : {}),
      },
    }))
    setError("")
  }

  function selectTemplate(runtimeId: string) {
    const nextTemplate = templates.find((item) => item.id === runtimeId)
    setInput((current) => ({
      ...current,
      spec: {
        ...current.spec,
        runtimeId,
        ...templateDefaults(nextTemplate),
        modelBindings: reconcileModelBindings(
          stringList(nextTemplate?.spec.credentialIds),
          credentials,
          {
            ...stringRecord(current.spec.modelBindings),
            ...stringRecord(nextTemplate?.spec.modelBindings),
          }
        ),
      },
    }))
    setError("")
  }

  function selectServer(serverId: string) {
    setInput((current) => {
      const nextServer = servers.find((item) => item.id === serverId)
      const supportedDrivers = runtimeDriverOptions(nextServer).filter(
        (option) => !option.disabled
      )
      const currentDriver = stringValue(current.spec.driver)
      const nextDriver = supportedDrivers.some(
        (option) => option.value === currentDriver
      )
        ? currentDriver
        : (supportedDrivers[0]?.value ?? "")
      return {
        ...current,
        spec: {
          ...current.spec,
          serverId,
          driver: nextDriver,
          imageReference: normalizeRuntimeImageReference(
            nextServer,
            nextDriver,
            current.spec.imageReference
          ),
        },
      }
    })
    setError("")
  }

  function selectDriver(nextDriver: string) {
    setInput((current) => ({
      ...current,
      spec: {
        ...current.spec,
        driver: nextDriver,
        imageReference: normalizeRuntimeImageReference(
          server,
          nextDriver,
          current.spec.imageReference
        ),
      },
    }))
    setError("")
  }

  function selectCredentials(values: string[]) {
    setInput((current) => ({
      ...current,
      spec: {
        ...current.spec,
        credentialIds: values,
        modelBindings: reconcileModelBindings(
          values,
          credentials,
          stringRecord(current.spec.modelBindings)
        ),
      },
    }))
    setError("")
  }

  function updateModel(credentialId: string, modelId: string) {
    updateSpec("modelBindings", {
      ...modelBindings,
      [credentialId]: modelId,
    })
  }

  async function submit() {
    if (saving || !dependenciesReady) return
    if (!template) {
      setError("请选择一个可用的沙箱模板")
      return
    }
    if (!configurationReady) {
      setError("运行服务器当前不可用，或所选隔离驱动尚未通过 Worker 自检")
      return
    }
    if (input.spec.network === "none" && stringValue(input.spec.proxyId)) {
      setError("完全隔离网络不能同时使用代理")
      return
    }
    const environmentError = environmentVariablesError(
      input.spec.environmentVariables
    )
    if (environmentError) {
      setError(environmentError)
      return
    }
    const next: ResourceDraft = {
      ...input,
      projectId,
      spec: {
        ...input.spec,
        runtimeId: template.id,
        serverId: input.spec.serverId,
        credentialIds,
        modelBindings,
      },
    }
    if (resource?.kind === "sandbox") {
      next.spec = sandboxSpecForUpdate(next.spec, resource.spec)
    }
    const parsed = resourceInputSchema.safeParse(next)
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "请检查沙箱配置")
      return
    }
    for (const item of selectedCredentials) {
      if (!item.credential) {
        setError(`模型服务 ${item.id} 不存在或已停用`)
        focusModelField(item.id)
        return
      }
      if (item.credential.models.length === 0) {
        setError(`模型服务 ${item.credential.name} 还没有可选模型`)
        focusModelField(item.id)
        return
      }
      if (!modelBindings[item.id]) {
        setError(`请为 ${item.credential.name} 选择具体模型`)
        focusModelField(item.id)
        return
      }
    }
    const incompatibleTools = incompatibleAgentTools(
      input.spec.agentTools,
      selectedCredentials.flatMap((item) =>
        item.credential ? [item.credential.protocol] : []
      )
    )
    if (incompatibleTools.length > 0) {
      const labels = incompatibleTools.map(
        (tool) =>
          agentToolOptions.find((option) => option.value === tool)?.label ??
          tool
      )
      setError(`${labels.join("、")} 与当前所选模型服务的接口协议不兼容`)
      return
    }
    setSaving(true)
    setError("")
    try {
      await onSave(parsed.data)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  function focusModelField(credentialId: string) {
    window.requestAnimationFrame(() => {
      document.getElementById(`sandbox-model-${credentialId}`)?.focus()
    })
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent
        className="flex max-h-[min(860px,calc(100vh-2rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl"
        onEscapeKeyDown={(event) => {
          if (
            document.querySelector('[data-slot="combobox-content"][data-open]')
          ) {
            event.preventDefault()
          }
        }}
      >
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle>{resource ? "编辑沙箱" : "创建沙箱"}</DialogTitle>
          <DialogDescription>
            {resource
              ? "调整沙箱名称、追加 Agent 或切换具体模型。"
              : "选择一个沙箱模板，再确定这个沙箱实际使用的 Agent 与模型。"}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
          <FieldGroup className="gap-6">
            {dependencyStatus}
            <FieldSet>
              <FieldLegend>基本信息</FieldLegend>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="sandbox-name">名称</FieldLabel>
                  <Input
                    id="sandbox-name"
                    value={input.name}
                    placeholder="例如 Kimi 开发沙箱"
                    onChange={(event) => {
                      const name = event.target.value
                      setInput((current) => ({
                        ...current,
                        name,
                        id:
                          !resource &&
                          (!current.name ||
                            !current.id ||
                            current.id === createSlug(current.name))
                            ? uniqueSandboxId(name, resources)
                            : current.id,
                      }))
                      setError("")
                    }}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="sandbox-id">唯一标识</FieldLabel>
                  <Input
                    id="sandbox-id"
                    value={input.id}
                    disabled={Boolean(resource)}
                    placeholder="kimi-dev"
                    onChange={(event) => update("id", event.target.value)}
                  />
                </Field>
              </div>
              <Field>
                <FieldLabel htmlFor="sandbox-description">说明</FieldLabel>
                <Textarea
                  id="sandbox-description"
                  className="min-h-20"
                  value={input.description}
                  placeholder="这个沙箱用于什么工作？"
                  onChange={(event) =>
                    update("description", event.target.value)
                  }
                />
              </Field>
            </FieldSet>

            <Separator />

            <FieldSet>
              <FieldLegend>运行环境</FieldLegend>
              <Field>
                <FieldLabel htmlFor="sandbox-template">沙箱模板</FieldLabel>
                <Select
                  value={stringValue(input.spec.runtimeId)}
                  disabled={Boolean(resource)}
                  onValueChange={selectTemplate}
                >
                  <SelectTrigger id="sandbox-template" className="w-full">
                    <SelectValue placeholder="选择沙箱模板" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectLabel>可用模板</SelectLabel>
                      {templates.map((item) => (
                        <SelectItem key={item.id} value={item.id}>
                          {item.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  {resource
                    ? "实例创建后模板固定；名称、Agent 与模型仍可维护。"
                    : "模板负责服务器、镜像、隔离方式与基础能力。"}
                </FieldDescription>
              </Field>

              {!template && (
                <Alert>
                  <BoxIcon />
                  <AlertTitle>还没有可用模板</AlertTitle>
                  <AlertDescription>
                    请先到沙箱模板页面创建并启用一个模板。
                  </AlertDescription>
                </Alert>
              )}

              {template && (
                <div className="grid gap-4 rounded-xl border bg-muted/15 p-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="sandbox-server">运行服务器</FieldLabel>
                    <Select
                      value={stringValue(input.spec.serverId)}
                      disabled={Boolean(resource)}
                      onValueChange={selectServer}
                    >
                      <SelectTrigger id="sandbox-server" className="w-full">
                        <SelectValue placeholder="选择运行服务器" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>已接入服务器</SelectLabel>
                          {servers.map((item) => (
                            <SelectItem key={item.id} value={item.id}>
                              {item.name} ·{" "}
                              {item.status === "online" ? "在线" : "离线"}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="sandbox-driver">隔离类型</FieldLabel>
                    <Select
                      value={driver}
                      disabled={Boolean(resource) || !server}
                      onValueChange={selectDriver}
                    >
                      <SelectTrigger id="sandbox-driver" className="w-full">
                        <SelectValue placeholder="选择隔离类型" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>Worker 可用驱动</SelectLabel>
                          {driverOptions.map((option) => (
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
                    {server && (
                      <FieldDescription>
                        {runtimeDriverDescription(server, driver)}
                      </FieldDescription>
                    )}
                  </Field>

                  <Field className="sm:col-span-2">
                    <FieldLabel htmlFor="sandbox-image">系统镜像</FieldLabel>
                    <RuntimeImageCombobox
                      id="sandbox-image"
                      value={stringValue(input.spec.imageReference)}
                      choices={imageChoices}
                      driver={driver}
                      disabled={Boolean(resource) || !server}
                      onChange={(value) => updateSpec("imageReference", value)}
                    />
                    <FieldDescription aria-live="polite">
                      {runtimeImageDescription(driver)}
                    </FieldDescription>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="sandbox-cpu">CPU</FieldLabel>
                    <Input
                      id="sandbox-cpu"
                      value={stringValue(input.spec.cpu)}
                      disabled={Boolean(resource)}
                      placeholder="2"
                      onChange={(event) =>
                        updateSpec("cpu", event.target.value)
                      }
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="sandbox-memory">内存</FieldLabel>
                    <Input
                      id="sandbox-memory"
                      value={stringValue(input.spec.memory)}
                      disabled={Boolean(resource)}
                      placeholder="4 GiB"
                      onChange={(event) =>
                        updateSpec("memory", event.target.value)
                      }
                    />
                  </Field>
                  <Field
                    className="sm:col-span-2"
                    data-disabled={resource ? true : undefined}
                  >
                    <div className="flex items-center justify-between gap-4 rounded-lg border bg-background px-3 py-2.5">
                      <div className="min-w-0">
                        <FieldLabel htmlFor="sandbox-desktop">
                          图形桌面
                        </FieldLabel>
                        <FieldDescription>
                          在沙箱内运行 1440 × 900 XFCE
                          桌面，可在工作区中直接操作。要求 Debian/Ubuntu
                          系镜像，建议至少 4 GiB 内存。
                        </FieldDescription>
                      </div>
                      <Switch
                        id="sandbox-desktop"
                        checked={input.spec.desktop === true}
                        disabled={Boolean(resource)}
                        onCheckedChange={(checked) =>
                          updateSpec("desktop", checked)
                        }
                      />
                    </div>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="sandbox-network">网络策略</FieldLabel>
                    <Select
                      value={stringValue(input.spec.network)}
                      disabled={Boolean(resource)}
                      onValueChange={(value) => updateSpec("network", value)}
                    >
                      <SelectTrigger id="sandbox-network" className="w-full">
                        <SelectValue placeholder="选择网络策略" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>网络策略</SelectLabel>
                          <SelectItem value="none">完全隔离</SelectItem>
                          <SelectItem value="restricted">受限网络</SelectItem>
                          <SelectItem value="egress">允许出站</SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="sandbox-proxy">网络代理</FieldLabel>
                    <Select
                      value={stringValue(input.spec.proxyId) || "__none__"}
                      disabled={
                        Boolean(resource) || input.spec.network === "none"
                      }
                      onValueChange={(value) =>
                        updateSpec("proxyId", value === "__none__" ? "" : value)
                      }
                    >
                      <SelectTrigger id="sandbox-proxy" className="w-full">
                        <SelectValue placeholder="选择网络代理" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>环境网络代理</SelectLabel>
                          <SelectItem value="__none__">不使用代理</SelectItem>
                          {proxies
                            .filter(
                              (item) =>
                                item.enabled ||
                                item.id === stringValue(input.spec.proxyId)
                            )
                            .map((item) => (
                              <SelectItem
                                key={item.id}
                                value={item.id}
                                disabled={!item.enabled}
                              >
                                {item.name}
                                {item.enabled ? "" : " · 已停用"}
                              </SelectItem>
                            ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {input.spec.network === "none"
                        ? "完全隔离时不能使用代理。"
                        : driver === "boxlite" &&
                            input.spec.network === "restricted"
                          ? "只放行代理、直连地址和控制面。"
                          : "向环境内程序注入标准 HTTP(S) 与 ALL_PROXY 变量。"}
                    </FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="sandbox-workdir">工作目录</FieldLabel>
                    <Input
                      id="sandbox-workdir"
                      value={stringValue(input.spec.workdir)}
                      disabled={Boolean(resource)}
                      placeholder="/workspace"
                      onChange={(event) =>
                        updateSpec("workdir", event.target.value)
                      }
                    />
                  </Field>
                  <Field className="sm:col-span-2">
                    <FieldLabel htmlFor="sandbox-setup">初始化命令</FieldLabel>
                    <Textarea
                      id="sandbox-setup"
                      className="min-h-20 font-mono text-sm"
                      value={stringValue(input.spec.setup)}
                      disabled={Boolean(resource)}
                      placeholder="apt-get update && apt-get install -y git"
                      onChange={(event) =>
                        updateSpec("setup", event.target.value)
                      }
                    />
                  </Field>
                </div>
              )}

              {template && !configurationReady && (
                <Alert variant="destructive">
                  <ServerIcon />
                  <AlertTitle>当前运行配置无法创建沙箱</AlertTitle>
                  <AlertDescription>
                    {!server
                      ? "还没有选择运行服务器。"
                      : server.status !== "online"
                        ? "所选服务器当前离线。"
                        : !server.capabilities.includes(requiredCapability)
                          ? `${driverLabel(driver)} 尚未通过这台服务器的 Worker 自检。`
                          : "还没有选择系统镜像。"}
                  </AlertDescription>
                </Alert>
              )}

              {resource && (
                <FieldDescription>
                  运行服务器、隔离类型、镜像和资源规格在实例创建时确定；如需更换，请从模板创建新沙箱。
                </FieldDescription>
              )}
            </FieldSet>

            <Separator />

            <FieldSet>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <FieldLegend>沙箱内 Agent</FieldLegend>
                <span className="text-xs text-muted-foreground">
                  已选择 {stringList(input.spec.agentTools).length} 个
                </span>
              </div>
              <ToggleGroup
                type="multiple"
                variant="selection"
                size="sm"
                className="flex w-full flex-wrap justify-start"
                value={stringList(input.spec.agentTools)}
                onValueChange={(values) =>
                  updateSpec(
                    "agentTools",
                    Array.from(new Set([...lockedTools, ...values]))
                  )
                }
              >
                {agentToolOptions.map((option) => {
                  const selected = stringList(input.spec.agentTools).includes(
                    option.value
                  )
                  const locked = resource && lockedTools.includes(option.value)
                  return (
                    <ToggleGroupItem
                      key={option.value}
                      value={option.value}
                      disabled={Boolean(locked)}
                    >
                      {selected && <CheckIcon data-icon="inline-start" />}
                      {option.label}
                    </ToggleGroupItem>
                  )
                })}
              </ToggleGroup>
              <FieldDescription>
                {resource
                  ? "已安装的 Agent 会保留；可以继续追加，重启沙箱后完成安装。"
                  : "模板给出初始组合，你可以为这个沙箱单独增减。"}
              </FieldDescription>
            </FieldSet>

            <Separator />

            <FieldSet>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <FieldLegend>扩展能力</FieldLegend>
                <span className="text-xs text-muted-foreground">
                  创建时注入，修改后重启生效
                </span>
              </div>
              <CapabilitySelector
                label="预装 Skills"
                options={projectSkills}
                value={stringList(input.spec.skillIds)}
                onValueChange={(values) => updateSpec("skillIds", values)}
              />
              <CapabilitySelector
                label="预配 MCP Servers"
                options={projectMCPServers}
                value={stringList(input.spec.mcpServerIds)}
                onValueChange={(values) => updateSpec("mcpServerIds", values)}
              />
              <Field>
                <FieldLabel>环境变量</FieldLabel>
                <EnvironmentVariablesEditor
                  value={input.spec.environmentVariables}
                  onChange={(value) =>
                    updateSpec("environmentVariables", value)
                  }
                />
                <FieldDescription>
                  模板提供初始值；在这里修改后仅影响当前沙箱。
                </FieldDescription>
              </Field>
            </FieldSet>

            <Separator />

            <FieldSet>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <FieldLegend>运行模型</FieldLegend>
                <span className="text-xs text-muted-foreground">
                  在沙箱级别生效
                </span>
              </div>
              <Field>
                <FieldLabel>模型服务</FieldLabel>
                {enabledCredentials.length > 0 ? (
                  <ToggleGroup
                    type="multiple"
                    variant="selection"
                    size="sm"
                    className="flex w-full flex-wrap justify-start"
                    value={credentialIds}
                    onValueChange={selectCredentials}
                  >
                    {enabledCredentials.map((credential) => (
                      <ToggleGroupItem
                        key={credential.id}
                        value={credential.id}
                      >
                        {credentialIds.includes(credential.id) && (
                          <CheckIcon data-icon="inline-start" />
                        )}
                        {credential.name}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    当前项目还没有已启用的模型服务。
                  </p>
                )}
                <FieldDescription>
                  模板给出默认服务；这个沙箱可以单独增减并选择具体模型。
                </FieldDescription>
              </Field>

              {selectedCredentials.length === 0 ? (
                <Alert>
                  <KeyRoundIcon />
                  <AlertTitle>沙箱没有配置模型服务</AlertTitle>
                  <AlertDescription>
                    沙箱仍可创建，但 Agent 需要自行登录或后续补充模型服务。
                  </AlertDescription>
                </Alert>
              ) : (
                <div className="divide-y rounded-lg border">
                  {selectedCredentials.map(({ id, credential }) => {
                    const modelId = modelBindings[id] ?? ""
                    return (
                      <div
                        key={id}
                        className="grid gap-3 px-3 py-3 sm:grid-cols-[minmax(10rem,0.7fr)_minmax(15rem,1.3fr)] sm:items-center"
                      >
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-xs font-semibold">
                              {credential?.name.slice(0, 1).toUpperCase() ??
                                "?"}
                            </span>
                            <div className="min-w-0">
                              <p className="truncate text-sm font-medium">
                                {credential?.name ?? id}
                              </p>
                              <p className="truncate text-xs text-muted-foreground">
                                {credential?.protocol ?? "服务不可用"}
                              </p>
                            </div>
                          </div>
                        </div>
                        <Field
                          data-invalid={Boolean(error) && !modelId}
                          className="gap-1.5"
                        >
                          <Select
                            value={modelId}
                            disabled={
                              !credential || credential.models.length === 0
                            }
                            onValueChange={(nextModelId) =>
                              updateModel(id, nextModelId)
                            }
                          >
                            <SelectTrigger
                              id={`sandbox-model-${id}`}
                              className="w-full"
                              aria-label={`${credential?.name ?? id} 模型`}
                              aria-invalid={Boolean(error) && !modelId}
                            >
                              <SelectValue
                                placeholder={
                                  credential?.models.length
                                    ? "选择具体模型"
                                    : "请先获取模型列表"
                                }
                              />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectGroup>
                                <SelectLabel>
                                  {credential?.name ?? id}
                                </SelectLabel>
                                {credential?.models.map((model) => (
                                  <SelectItem key={model.id} value={model.id}>
                                    {model.name}
                                    {model.name !== model.id
                                      ? ` · ${model.id}`
                                      : ""}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                          {error && !modelId && (
                            <FieldError>
                              {credential?.models.length
                                ? "请选择具体模型。"
                                : "该模型服务没有可用模型。"}
                            </FieldError>
                          )}
                        </Field>
                      </div>
                    )
                  })}
                </div>
              )}
              <FieldDescription>
                模型服务只保存连接与模型目录；这里的选择会转换成各 Agent
                需要的配置格式。
              </FieldDescription>
            </FieldSet>

            {resource && (
              <Alert>
                <LoaderCircleIcon />
                <AlertTitle>如何应用运行配置</AlertTitle>
                <AlertDescription>
                  名称会立即更新；新增 Agent
                  或模型变更会在下次启动或重启沙箱时注入。
                </AlertDescription>
              </Alert>
            )}

            {error && <FieldError>{error}</FieldError>}
          </FieldGroup>
        </div>

        <DialogFooter className="border-t px-6 py-4">
          <Button
            variant="outline"
            disabled={saving}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button
            disabled={
              saving || !dependenciesReady || !template || !configurationReady
            }
            onClick={() => void submit()}
          >
            {saving && <LoaderCircleIcon className="animate-spin" />}
            {saving ? "正在保存…" : resource ? "保存修改" : "创建沙箱"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CapabilitySelector({
  label,
  options,
  value,
  onValueChange,
}: {
  label: string
  options: Array<{ value: string; label: string }>
  value: string[]
  onValueChange: (value: string[]) => void
}) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {options.length > 0 ? (
        <ToggleGroup
          type="multiple"
          variant="selection"
          size="sm"
          className="flex w-full flex-wrap justify-start"
          value={value}
          onValueChange={onValueChange}
        >
          {options.map((option) => (
            <ToggleGroupItem key={option.value} value={option.value}>
              {value.includes(option.value) && (
                <CheckIcon data-icon="inline-start" />
              )}
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      ) : (
        <p className="text-sm text-muted-foreground">当前项目暂无可选配置。</p>
      )}
    </Field>
  )
}

function createSandboxInput(
  projectId: string,
  resources: Resource[],
  template: ResourceOfKind<"runtime"> | undefined,
  credentials: ManagedCredential[]
): ResourceDraft {
  return {
    id: uniqueSandboxId("sandbox", resources),
    kind: "sandbox",
    projectId,
    name: "",
    description: "",
    enabled: true,
    spec: {
      policy: "new",
      status: "requested",
      runtimeId: template?.id ?? "",
      ...templateDefaults(template),
      modelBindings: reconcileModelBindings(
        stringList(template?.spec.credentialIds),
        credentials,
        stringRecord(template?.spec.modelBindings)
      ),
      workspace: "",
    },
  }
}

function sandboxInputFromResource(
  resource: Resource,
  template: ResourceOfKind<"runtime"> | undefined,
  credentials: ManagedCredential[]
): ResourceDraft {
  const input: ResourceDraft = { ...resource, spec: { ...resource.spec } }
  return {
    ...input,
    spec: {
      ...templateDefaults(template),
      ...input.spec,
      agentTools: supportedAgentToolList(input.spec.agentTools),
      modelBindings: reconcileModelBindings(
        stringList(input.spec.credentialIds),
        credentials,
        {
          ...stringRecord(template?.spec.modelBindings),
          ...stringRecord(input.spec.modelBindings),
        }
      ),
      environmentVariables: sandboxEnvironmentVariables(
        input.spec.environmentVariables
      ),
    },
  }
}

function templateDefaults(template?: ResourceOfKind<"runtime">) {
  return {
    serverId: stringValue(template?.spec.serverId),
    driver: stringValue(template?.spec.driver),
    imageReference: stringValue(template?.spec.imageReference),
    imageId: stringValue(template?.spec.imageId),
    workdir: stringValue(template?.spec.workdir) || "/workspace",
    setup: stringValue(template?.spec.setup),
    cpu: stringValue(template?.spec.cpu) || "2",
    memory: stringValue(template?.spec.memory) || "4 GiB",
    desktop: template?.spec.desktop,
    network: stringValue(template?.spec.network) || "restricted",
    proxyId: stringValue(template?.spec.proxyId),
    agentTools: supportedAgentToolList(template?.spec.agentTools),
    skillIds: stringList(template?.spec.skillIds),
    mcpServerIds: stringList(template?.spec.mcpServerIds),
    variableIds: stringList(template?.spec.variableIds),
    environmentVariables: sandboxEnvironmentVariables(
      template?.spec.environmentVariables
    ),
    credentialIds: stringList(template?.spec.credentialIds),
  }
}

function runtimeDriverOptions(server?: ManagedServer) {
  if (!server) return []
  return [
    {
      value: "docker",
      label: server.capabilities.includes("docker")
        ? "Docker 容器（推荐）"
        : "Docker 容器 · Worker 未就绪",
      disabled: !server.capabilities.includes("docker"),
    },
    {
      value: "boxlite",
      label: server.capabilities.includes("boxlite")
        ? "BoxLite MicroVM"
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
    return "已检测到 KVM，但 MicroVM SDK/驱动尚未安装或未通过 Worker 自检。"
  }
  if (driver === "boxlite") {
    return "使用 BoxLite SDK 创建独立 MicroVM。"
  }
  if (driver === "microsandbox") {
    return "使用 Microsandbox SDK 创建实验性 MicroVM。"
  }
  return "使用 Docker 创建兼容性优先的容器环境。"
}

function runtimeImageDescription(driver: string) {
  if (driver === "boxlite") {
    return "可搜索当前服务器的容器镜像；未缓存的 Registry 引用由 BoxLite 在创建时拉取。"
  }
  if (driver === "microsandbox") {
    return "可搜索当前服务器的容器镜像；Microsandbox 会导入可复用内容或从 Registry 拉取。"
  }
  if (driver === "vm") {
    return "只显示 Worker 已盘点到的本地 qcow2/raw VM 镜像。"
  }
  return "可搜索当前服务器的容器镜像；未缓存的 Registry 引用会在创建时拉取。"
}

function projectResources(
  resources: Resource[],
  projectId: string,
  kind: "skill" | "mcp"
) {
  return resources
    .filter(
      (item) =>
        item.kind === kind && item.projectId === projectId && item.enabled
    )
    .map((item) => ({ value: item.id, label: item.name }))
}

function uniqueSandboxId(name: string, resources: Resource[]) {
  const base = createSlug(name)
  if (!resources.some((item) => item.id === base)) return base
  let index = 2
  while (resources.some((item) => item.id === `${base}-${index}`)) index += 1
  return `${base}-${index}`
}

function createSlug(value: string) {
  return (
    value
      .normalize("NFKD")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 64) || "sandbox"
  )
}

function stringList(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}

function stringRecord(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {} as Record<string, string>
  }
  return Object.fromEntries(
    Object.entries(value).filter(
      (entry): entry is [string, string] => typeof entry[1] === "string"
    )
  )
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : ""
}

function driverLabel(driver: string) {
  if (driver === "docker") return "Docker"
  if (driver === "boxlite") return "BoxLite MicroVM"
  if (driver === "microsandbox") return "Microsandbox"
  if (driver === "vm") return "VM（旧版）"
  return "未配置"
}

function isTemplateReady(
  template: ResourceOfKind<"runtime">,
  servers: ManagedServer[]
) {
  const server = servers.find((item) => item.id === template.spec.serverId)
  const driver = stringValue(template.spec.driver)
  const requiredCapability = driver === "vm" ? "kvm" : driver
  return Boolean(
    server?.status === "online" &&
    server.capabilities.includes(requiredCapability) &&
    stringValue(template.spec.imageReference)
  )
}
