"use client"

import { useMemo, useState } from "react"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BotIcon,
  CheckIcon,
  ContainerIcon,
  KeyRoundIcon,
  PlugZapIcon,
  SaveIcon,
  SparklesIcon,
} from "lucide-react"
import type { ZodIssue } from "zod"

import {
  agentInputSchema,
  createSlug,
  type Agent,
  type AgentInput,
} from "@/lib/agent-schema"
import type {
  Credential,
  McpServerDefinition,
  Provider,
  SkillDefinition,
} from "@/lib/catalog"
import type { Resource } from "@/lib/platform-schema"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
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
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { ScrollArea } from "@/components/ui/scroll-area"
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
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type Catalog = {
  providers: Provider[]
  credentials: Credential[]
  skills: SkillDefinition[]
  mcpServers: McpServerDefinition[]
  projects: Resource[]
  runtimes: Resource[]
  variables: Resource[]
}

const steps = [
  { title: "身份", description: "名称与用途" },
  { title: "环境", description: "Project 与 Runtime" },
  { title: "模型", description: "Provider 与指令" },
  { title: "能力", description: "Skills 与 MCP" },
  { title: "发布", description: "参数与状态" },
]

function defaultInput(catalog: Catalog): AgentInput {
  const provider = catalog.providers[0]
  const credential = catalog.credentials.find(
    (item) => item.providerId === provider.id
  )
  return {
    projectId: catalog.projects[0]?.id ?? "default",
    runtimeId: catalog.runtimes[0]?.id ?? "python-venv",
    name: "",
    slug: "",
    description: "",
    avatar: "A",
    providerId: provider.id,
    modelId: provider.models[0].id,
    credentialId: credential?.id ?? null,
    systemPrompt: "",
    skillIds: [],
    mcpServerIds: [],
    variableIds: [],
    customArgs: [],
    temperature: 0.4,
    maxSteps: 12,
    concurrency: 1,
    sandboxPolicy: "new",
    status: "draft",
  }
}

function fromAgent(agent: Agent): AgentInput {
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

function issuesToErrors(issues: ZodIssue[]) {
  return Object.fromEntries(
    issues.map((issue) => [String(issue.path[0] ?? "form"), issue.message])
  )
}

export function AgentEditorDialog({
  agent,
  catalog,
  onOpenChange,
  onSave,
}: {
  agent: Agent | null
  catalog: Catalog
  onOpenChange: (open: boolean) => void
  onSave: (input: AgentInput, agent: Agent | null) => Promise<void>
}) {
  const [step, setStep] = useState(0)
  const [input, setInput] = useState<AgentInput>(() =>
    agent ? fromAgent(agent) : defaultInput(catalog)
  )
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState("")
  const [saving, setSaving] = useState(false)
  const [slugEdited, setSlugEdited] = useState(Boolean(agent))

  const provider = useMemo(
    () => catalog.providers.find((item) => item.id === input.providerId)!,
    [catalog.providers, input.providerId]
  )
  const matchingCredentials = catalog.credentials.filter(
    (item) => item.providerId === input.providerId
  )

  function update<K extends keyof AgentInput>(key: K, value: AgentInput[K]) {
    setInput((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: "" }))
    setFormError("")
  }

  function chooseProvider(providerId: string) {
    const nextProvider = catalog.providers.find(
      (item) => item.id === providerId
    )!
    const credential = catalog.credentials.find(
      (item) => item.providerId === providerId
    )
    setInput((current) => ({
      ...current,
      providerId,
      modelId: nextProvider.models[0].id,
      credentialId: credential?.id ?? null,
    }))
    setErrors((current) => ({
      ...current,
      providerId: "",
      modelId: "",
      credentialId: "",
    }))
  }

  function toggleList(
    key: "skillIds" | "mcpServerIds",
    id: string,
    checked: boolean
  ) {
    update(
      key,
      checked ? [...input[key], id] : input[key].filter((item) => item !== id)
    )
  }

  function validateCurrentStep() {
    const keys: Array<Array<keyof AgentInput>> = [
      ["name", "slug", "description", "avatar"],
      [
        "projectId",
        "runtimeId",
        "variableIds",
        "customArgs",
        "concurrency",
        "sandboxPolicy",
      ],
      ["providerId", "modelId", "credentialId", "systemPrompt"],
      ["skillIds", "mcpServerIds"],
      ["temperature", "maxSteps", "status"],
    ]
    const result = agentInputSchema.safeParse(input)
    if (result.success) {
      setErrors({})
      return true
    }

    const relevant = result.error.issues.filter((issue) =>
      keys[step].includes(issue.path[0] as keyof AgentInput)
    )
    if (relevant.length === 0) return true
    setErrors(issuesToErrors(relevant))
    return false
  }

  function nextStep() {
    if (!validateCurrentStep()) return
    setStep((current) => Math.min(current + 1, steps.length - 1))
  }

  async function submit() {
    const parsed = agentInputSchema.safeParse(input)
    if (!parsed.success) {
      setErrors(issuesToErrors(parsed.error.issues))
      const firstError = parsed.error.issues[0]?.path[0]
      if (
        ["name", "slug", "description", "avatar"].includes(String(firstError))
      )
        setStep(0)
      else if (
        [
          "projectId",
          "runtimeId",
          "variableIds",
          "customArgs",
          "concurrency",
          "sandboxPolicy",
        ].includes(String(firstError))
      )
        setStep(1)
      else if (
        ["providerId", "modelId", "credentialId", "systemPrompt"].includes(
          String(firstError)
        )
      )
        setStep(2)
      else if (["skillIds", "mcpServerIds"].includes(String(firstError)))
        setStep(3)
      return
    }

    setSaving(true)
    setFormError("")
    try {
      await onSave(parsed.data, agent)
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !saving && onOpenChange(open)}>
      <DialogContent className="flex max-h-[92svh] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="border-b px-5 py-4 sm:px-6">
          <DialogTitle>
            {agent ? `编辑 ${agent.name}` : "创建 Agent"}
          </DialogTitle>
          <DialogDescription>
            声明 Agent、运行环境和能力；保存配置不会立即启动 Sandbox。
          </DialogDescription>
        </DialogHeader>

        <div className="border-b px-5 py-3 sm:px-6">
          <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
            <span>
              第 {step + 1} 步，共 {steps.length} 步
            </span>
            <span>
              {steps[step].title} · {steps[step].description}
            </span>
          </div>
          <Progress value={((step + 1) / steps.length) * 100} />
          <ol className="mt-3 hidden grid-cols-5 gap-2 sm:grid">
            {steps.map((item, index) => (
              <li
                key={item.title}
                className={
                  index <= step ? "text-foreground" : "text-muted-foreground"
                }
              >
                <div className="flex items-center gap-2 text-xs font-medium">
                  <span
                    className={
                      index <= step
                        ? "flex size-5 items-center justify-center rounded-full bg-primary text-primary-foreground"
                        : "flex size-5 items-center justify-center rounded-full bg-muted"
                    }
                  >
                    {index < step ? <CheckIcon /> : index + 1}
                  </span>
                  {item.title}
                </div>
              </li>
            ))}
          </ol>
        </div>

        <ScrollArea className="min-h-0 flex-1">
          <div className="px-5 py-5 sm:px-6">
            {formError && (
              <Alert variant="destructive" className="mb-5">
                <AlertTitle>无法保存</AlertTitle>
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            )}

            {step === 0 && (
              <FieldGroup>
                <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_9rem]">
                  <Field data-invalid={Boolean(errors.name)}>
                    <FieldLabel htmlFor="agent-name">名称</FieldLabel>
                    <Input
                      id="agent-name"
                      autoFocus
                      value={input.name}
                      aria-invalid={Boolean(errors.name)}
                      placeholder="例如：Research Copilot"
                      onChange={(event) => {
                        const name = event.target.value
                        update("name", name)
                        if (!slugEdited) update("slug", createSlug(name))
                        if (!agent)
                          update(
                            "avatar",
                            name.trim().slice(0, 2).toUpperCase() || "A"
                          )
                      }}
                    />
                    <FieldDescription>
                      显示在平台列表和后续运行记录中的名称。
                    </FieldDescription>
                    <FieldError>{errors.name}</FieldError>
                  </Field>
                  <Field data-invalid={Boolean(errors.avatar)}>
                    <FieldLabel htmlFor="agent-avatar">头像文字</FieldLabel>
                    <Input
                      id="agent-avatar"
                      value={input.avatar}
                      aria-invalid={Boolean(errors.avatar)}
                      maxLength={4}
                      onChange={(event) =>
                        update("avatar", event.target.value.toUpperCase())
                      }
                    />
                    <FieldError>{errors.avatar}</FieldError>
                  </Field>
                </div>

                <Field data-invalid={Boolean(errors.slug)}>
                  <FieldLabel htmlFor="agent-slug">唯一标识</FieldLabel>
                  <Input
                    id="agent-slug"
                    value={input.slug}
                    aria-invalid={Boolean(errors.slug)}
                    placeholder="research-copilot"
                    onChange={(event) => {
                      setSlugEdited(true)
                      update("slug", event.target.value.toLowerCase())
                    }}
                  />
                  <FieldDescription>
                    用于 API 与后续运行引用，创建后仍可修改。
                  </FieldDescription>
                  <FieldError>{errors.slug}</FieldError>
                </Field>

                <Field data-invalid={Boolean(errors.description)}>
                  <FieldLabel htmlFor="agent-description">简介</FieldLabel>
                  <Textarea
                    id="agent-description"
                    value={input.description}
                    aria-invalid={Boolean(errors.description)}
                    placeholder="一句话说明这个 Agent 解决什么问题。"
                    className="min-h-24 resize-none"
                    onChange={(event) =>
                      update("description", event.target.value)
                    }
                  />
                  <div className="flex justify-between gap-4">
                    <FieldDescription>
                      可选，但建议写清使用边界。
                    </FieldDescription>
                    <span className="text-xs text-muted-foreground">
                      {input.description.length}/280
                    </span>
                  </div>
                  <FieldError>{errors.description}</FieldError>
                </Field>
              </FieldGroup>
            )}

            {step === 1 && (
              <FieldGroup>
                <div className="flex items-start gap-3 rounded-xl border bg-muted/30 p-4">
                  <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-foreground text-background">
                    <ContainerIcon />
                  </div>
                  <div>
                    <p className="font-medium">运行声明</p>
                    <p className="mt-1 text-sm text-muted-foreground">
                      Agent 绑定模板；Sandbox 启动时才创建实际隔离环境。
                    </p>
                  </div>
                </div>
                <div className="grid gap-5 sm:grid-cols-2">
                  <Field data-invalid={Boolean(errors.projectId)}>
                    <FieldLabel>Project</FieldLabel>
                    <Select
                      value={input.projectId}
                      onValueChange={(value) => update("projectId", value)}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder="选择项目" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>Projects</SelectLabel>
                          {catalog.projects.map((item) => (
                            <SelectItem key={item.id} value={item.id}>
                              {item.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldError>{errors.projectId}</FieldError>
                  </Field>
                  <Field data-invalid={Boolean(errors.runtimeId)}>
                    <FieldLabel>Runtime</FieldLabel>
                    <Select
                      value={input.runtimeId}
                      onValueChange={(value) => update("runtimeId", value)}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder="选择 Runtime" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>Runtime templates</SelectLabel>
                          {catalog.runtimes
                            .filter(
                              (item) => item.projectId === input.projectId
                            )
                            .map((item) => (
                              <SelectItem key={item.id} value={item.id}>
                                {item.name} ·{" "}
                                {String(item.spec.driver ?? "runtime")}
                              </SelectItem>
                            ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldError>{errors.runtimeId}</FieldError>
                  </Field>
                  <Field>
                    <FieldLabel>Sandbox 策略</FieldLabel>
                    <Select
                      value={input.sandboxPolicy}
                      onValueChange={(value) =>
                        update(
                          "sandboxPolicy",
                          value as AgentInput["sandboxPolicy"]
                        )
                      }
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>生命周期</SelectLabel>
                          <SelectItem value="new">new · 每次新建</SelectItem>
                          <SelectItem value="reuse">
                            reuse · 优先复用
                          </SelectItem>
                          <SelectItem value="sticky">
                            sticky · 固定实例
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel>并发上限</FieldLabel>
                    <Select
                      value={String(input.concurrency)}
                      onValueChange={(value) =>
                        update("concurrency", Number(value))
                      }
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>同时运行</SelectLabel>
                          {[1, 2, 4, 8].map((value) => (
                            <SelectItem key={value} value={String(value)}>
                              {value} 个 Sandbox
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </Field>
                </div>
                <Field>
                  <FieldLabel>环境变量与 Secret 引用</FieldLabel>
                  <FieldGroup className="grid gap-3 sm:grid-cols-2">
                    {catalog.variables
                      .filter((item) => item.projectId === input.projectId)
                      .map((variable) => (
                        <Field
                          key={variable.id}
                          orientation="horizontal"
                          className="rounded-xl border p-3 has-data-checked:border-primary"
                        >
                          <Checkbox
                            id={`variable-${variable.id}`}
                            checked={input.variableIds.includes(variable.id)}
                            onCheckedChange={(checked) =>
                              update(
                                "variableIds",
                                checked === true
                                  ? [...input.variableIds, variable.id]
                                  : input.variableIds.filter(
                                      (id) => id !== variable.id
                                    )
                              )
                            }
                          />
                          <FieldContent>
                            <FieldLabel htmlFor={`variable-${variable.id}`}>
                              {String(variable.spec.key ?? variable.name)}
                            </FieldLabel>
                            <FieldDescription>
                              {String(
                                variable.spec.reference ?? variable.description
                              )}
                            </FieldDescription>
                          </FieldContent>
                        </Field>
                      ))}
                  </FieldGroup>
                </Field>
                <Field>
                  <FieldLabel htmlFor="agent-custom-args">
                    自定义启动参数
                  </FieldLabel>
                  <Textarea
                    id="agent-custom-args"
                    className="min-h-24 font-mono text-sm"
                    value={input.customArgs.join("\n")}
                    placeholder="--profile&#10;research"
                    onChange={(event) =>
                      update(
                        "customArgs",
                        event.target.value
                          .split("\n")
                          .map((value) => value.trim())
                          .filter(Boolean)
                      )
                    }
                  />
                  <FieldDescription>
                    每行一个参数，启动 Provider CLI 时按顺序传入。
                  </FieldDescription>
                </Field>
              </FieldGroup>
            )}

            {step === 2 && (
              <FieldGroup>
                <Field data-invalid={Boolean(errors.providerId)}>
                  <FieldLabel>Provider</FieldLabel>
                  <RadioGroup
                    value={input.providerId}
                    onValueChange={chooseProvider}
                    className="grid gap-3 sm:grid-cols-2"
                  >
                    {catalog.providers.map((item) => (
                      <label
                        key={item.id}
                        htmlFor={`provider-${item.id}`}
                        className="flex cursor-pointer gap-3 rounded-xl border p-3 transition-colors hover:bg-muted/50 has-data-checked:border-primary has-data-checked:bg-muted/50"
                      >
                        <RadioGroupItem
                          id={`provider-${item.id}`}
                          value={item.id}
                          className="mt-0.5"
                        />
                        <span className="min-w-0">
                          <span className="flex items-center gap-2 font-medium">
                            <span className="flex size-7 items-center justify-center rounded-md bg-foreground text-[10px] text-background">
                              {item.mark}
                            </span>
                            {item.name}
                          </span>
                          <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
                            {item.description}
                          </span>
                        </span>
                      </label>
                    ))}
                  </RadioGroup>
                  <FieldError>{errors.providerId}</FieldError>
                </Field>

                <div className="grid gap-5 sm:grid-cols-2">
                  <Field data-invalid={Boolean(errors.modelId)}>
                    <FieldLabel>模型</FieldLabel>
                    <Select
                      value={input.modelId}
                      onValueChange={(value) => update("modelId", value)}
                    >
                      <SelectTrigger
                        className="w-full"
                        aria-invalid={Boolean(errors.modelId)}
                      >
                        <SelectValue placeholder="选择模型" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>{provider.name}</SelectLabel>
                          {provider.models.map((model) => (
                            <SelectItem key={model.id} value={model.id}>
                              {model.name} · {model.note}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {
                        provider.models.find(
                          (item) => item.id === input.modelId
                        )?.context
                      }
                    </FieldDescription>
                    <FieldError>{errors.modelId}</FieldError>
                  </Field>

                  <Field data-invalid={Boolean(errors.credentialId)}>
                    <FieldLabel>凭据引用</FieldLabel>
                    <Select
                      value={input.credentialId ?? ""}
                      onValueChange={(value) => update("credentialId", value)}
                    >
                      <SelectTrigger
                        className="w-full"
                        aria-invalid={Boolean(errors.credentialId)}
                      >
                        <SelectValue placeholder="选择凭据槽位" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>仅保存引用</SelectLabel>
                          {matchingCredentials.map((credential) => (
                            <SelectItem
                              key={credential.id}
                              value={credential.id}
                            >
                              {credential.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      不在 Agent 记录中保存 API Key。
                    </FieldDescription>
                    <FieldError>{errors.credentialId}</FieldError>
                  </Field>
                </div>

                <Field data-invalid={Boolean(errors.systemPrompt)}>
                  <FieldLabel htmlFor="agent-prompt">系统指令</FieldLabel>
                  <Textarea
                    id="agent-prompt"
                    value={input.systemPrompt}
                    aria-invalid={Boolean(errors.systemPrompt)}
                    placeholder="说明角色、目标、工作方法、边界和输出要求。"
                    className="min-h-48 resize-y font-mono text-sm"
                    onChange={(event) =>
                      update("systemPrompt", event.target.value)
                    }
                  />
                  <div className="flex justify-between gap-4">
                    <FieldDescription>
                      这是 Agent 行为的主要声明，不与 Runtime 绑定。
                    </FieldDescription>
                    <span className="text-xs text-muted-foreground">
                      {input.systemPrompt.length}/16000
                    </span>
                  </div>
                  <FieldError>{errors.systemPrompt}</FieldError>
                </Field>
              </FieldGroup>
            )}

            {step === 3 && (
              <div className="grid gap-7">
                <div>
                  <div className="mb-3 flex items-start gap-3">
                    <div className="flex size-9 items-center justify-center rounded-lg bg-muted">
                      <SparklesIcon />
                    </div>
                    <div>
                      <h3 className="font-medium">Skills</h3>
                      <p className="text-sm text-muted-foreground">
                        声明 Agent 可使用的知识、流程和模板。
                      </p>
                    </div>
                  </div>
                  <FieldGroup className="grid gap-3 sm:grid-cols-2">
                    {catalog.skills.map((skill) => (
                      <Field
                        key={skill.id}
                        orientation="horizontal"
                        className="rounded-xl border p-3 has-data-checked:border-primary has-data-checked:bg-muted/40"
                      >
                        <Checkbox
                          id={`skill-${skill.id}`}
                          checked={input.skillIds.includes(skill.id)}
                          onCheckedChange={(checked) =>
                            toggleList("skillIds", skill.id, checked === true)
                          }
                        />
                        <FieldContent>
                          <FieldLabel htmlFor={`skill-${skill.id}`}>
                            <span className="flex w-full items-center justify-between gap-2">
                              <span>{skill.name}</span>
                              <Badge variant="secondary">
                                {skill.category}
                              </Badge>
                            </span>
                          </FieldLabel>
                          <FieldDescription>
                            {skill.description}
                          </FieldDescription>
                          <span className="text-xs text-muted-foreground">
                            v{skill.version}
                          </span>
                        </FieldContent>
                      </Field>
                    ))}
                  </FieldGroup>
                </div>

                <Separator />

                <div>
                  <div className="mb-3 flex items-start gap-3">
                    <div className="flex size-9 items-center justify-center rounded-lg bg-muted">
                      <PlugZapIcon />
                    </div>
                    <div>
                      <h3 className="font-medium">MCP Servers</h3>
                      <p className="text-sm text-muted-foreground">
                        声明 Agent 可以访问的外部工具连接。
                      </p>
                    </div>
                  </div>
                  <FieldGroup className="grid gap-3 sm:grid-cols-2">
                    {catalog.mcpServers.map((server) => (
                      <Field
                        key={server.id}
                        orientation="horizontal"
                        className="rounded-xl border p-3 has-data-checked:border-primary has-data-checked:bg-muted/40"
                      >
                        <Checkbox
                          id={`mcp-${server.id}`}
                          checked={input.mcpServerIds.includes(server.id)}
                          disabled={server.status === "attention"}
                          onCheckedChange={(checked) =>
                            toggleList(
                              "mcpServerIds",
                              server.id,
                              checked === true
                            )
                          }
                        />
                        <FieldContent>
                          <FieldLabel htmlFor={`mcp-${server.id}`}>
                            <span className="flex w-full items-center justify-between gap-2">
                              <span>{server.name}</span>
                              <Badge
                                variant={
                                  server.status === "ready"
                                    ? "secondary"
                                    : "outline"
                                }
                              >
                                {server.transport}
                              </Badge>
                            </span>
                          </FieldLabel>
                          <FieldDescription>
                            {server.description}
                          </FieldDescription>
                          <span className="text-xs text-muted-foreground">
                            {server.status === "ready"
                              ? `${server.toolCount} 个工具`
                              : "需要先完成连接配置"}
                          </span>
                        </FieldContent>
                      </Field>
                    ))}
                  </FieldGroup>
                </div>
              </div>
            )}

            {step === 4 && (
              <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
                <FieldGroup>
                  <Field>
                    <FieldLabel>保存状态</FieldLabel>
                    <ToggleGroup
                      type="single"
                      variant="outline"
                      value={
                        input.status === "archived" ? "draft" : input.status
                      }
                      onValueChange={(value) =>
                        value && update("status", value as "draft" | "active")
                      }
                      className="w-full"
                    >
                      <ToggleGroupItem value="draft" className="flex-1">
                        保存为草稿
                      </ToggleGroupItem>
                      <ToggleGroupItem value="active" className="flex-1">
                        立即启用
                      </ToggleGroupItem>
                    </ToggleGroup>
                    <FieldDescription>
                      启用表示配置可被后续 Runtime 选择，不代表已开始运行。
                    </FieldDescription>
                  </Field>

                  <div className="grid gap-5 sm:grid-cols-2">
                    <Field>
                      <FieldLabel>创造性</FieldLabel>
                      <Select
                        value={String(input.temperature)}
                        onValueChange={(value) =>
                          update("temperature", Number(value))
                        }
                      >
                        <SelectTrigger className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectLabel>Temperature</SelectLabel>
                            <SelectItem value="0.2">稳定 · 0.2</SelectItem>
                            <SelectItem value="0.4">均衡 · 0.4</SelectItem>
                            <SelectItem value="0.8">灵活 · 0.8</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field>
                      <FieldLabel>最大步骤</FieldLabel>
                      <Select
                        value={String(input.maxSteps)}
                        onValueChange={(value) =>
                          update("maxSteps", Number(value))
                        }
                      >
                        <SelectTrigger className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectLabel>单次运行上限</SelectLabel>
                            <SelectItem value="6">6 步 · 简单任务</SelectItem>
                            <SelectItem value="12">12 步 · 标准</SelectItem>
                            <SelectItem value="20">20 步 · 复杂任务</SelectItem>
                            <SelectItem value="30">30 步 · 深度工作</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </Field>
                  </div>

                  <Alert>
                    <KeyRoundIcon />
                    <AlertTitle>凭据隔离</AlertTitle>
                    <AlertDescription>
                      Agent 只保存凭据 ID。密钥注入、权限检查与调用审计属于后续
                      Runtime 层。
                    </AlertDescription>
                  </Alert>
                </FieldGroup>

                <Card size="sm" className="h-fit">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <BotIcon /> 配置摘要
                    </CardTitle>
                    <CardDescription>保存前最后确认</CardDescription>
                  </CardHeader>
                  <CardContent className="grid gap-4">
                    <div>
                      <p className="text-xs text-muted-foreground">Agent</p>
                      <p className="font-medium">{input.name || "未命名"}</p>
                      <p className="text-xs text-muted-foreground">
                        /{input.slug || "agent"}
                      </p>
                    </div>
                    <Separator />
                    <div className="grid grid-cols-2 gap-3 text-sm">
                      <div>
                        <p className="text-xs text-muted-foreground">
                          Provider
                        </p>
                        <p>{provider.name}</p>
                      </div>
                      <div>
                        <p className="text-xs text-muted-foreground">模型</p>
                        <p>
                          {
                            provider.models.find(
                              (model) => model.id === input.modelId
                            )?.name
                          }
                        </p>
                      </div>
                      <div>
                        <p className="text-xs text-muted-foreground">Skills</p>
                        <p>{input.skillIds.length} 个</p>
                      </div>
                      <div>
                        <p className="text-xs text-muted-foreground">MCP</p>
                        <p>{input.mcpServerIds.length} 个</p>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              </div>
            )}
          </div>
        </ScrollArea>

        <DialogFooter className="border-t bg-muted/30 px-5 py-3 sm:px-6">
          <div className="flex w-full items-center justify-between gap-3">
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                step === 0
                  ? onOpenChange(false)
                  : setStep((current) => current - 1)
              }
              disabled={saving}
            >
              {step === 0 ? (
                "取消"
              ) : (
                <>
                  <ArrowLeftIcon data-icon="inline-start" />
                  上一步
                </>
              )}
            </Button>
            {step < steps.length - 1 ? (
              <Button type="button" onClick={nextStep}>
                下一步
                <ArrowRightIcon data-icon="inline-end" />
              </Button>
            ) : (
              <Button type="button" onClick={submit} disabled={saving}>
                {saving ? (
                  <Spinner data-icon="inline-start" />
                ) : (
                  <SaveIcon data-icon="inline-start" />
                )}
                {agent
                  ? "保存更改"
                  : input.status === "active"
                    ? "创建并启用"
                    : "创建草稿"}
              </Button>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
