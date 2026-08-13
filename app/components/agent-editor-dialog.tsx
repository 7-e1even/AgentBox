"use client"

import { useMemo, useState } from "react"
import { BotIcon, SaveIcon } from "lucide-react"
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

function defaultInput(catalog: Catalog, projectId: string): AgentInput {
  const project = catalog.projects.find((item) => item.id === projectId)
  const provider = catalog.providers[0]
  const credential = catalog.credentials.find(
    (item) => item.providerId === provider.id && item.status === "configured"
  )
  const models = credential?.models.length ? credential.models : provider.models
  const modelId =
    credential?.modelId &&
    models.some((model) => model.id === credential.modelId)
      ? credential.modelId
      : (models[0]?.id ?? "")
  return {
    projectId: project?.id ?? "default",
    runtimeId:
      catalog.runtimes.find((item) => item.projectId === project?.id)?.id ?? "",
    name: "",
    slug: "agent",
    description: "",
    avatar: "A",
    providerId: provider.id,
    modelId,
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
  projectId,
  onOpenChange,
  onSave,
}: {
  agent: Agent | null
  catalog: Catalog
  projectId: string
  onOpenChange: (open: boolean) => void
  onSave: (input: AgentInput, agent: Agent | null) => Promise<void>
}) {
  const [input, setInput] = useState<AgentInput>(() =>
    agent ? fromAgent(agent) : defaultInput(catalog, projectId)
  )
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState("")
  const [saving, setSaving] = useState(false)
  const [slugEdited, setSlugEdited] = useState(Boolean(agent))

  const provider = useMemo(
    () => catalog.providers.find((item) => item.id === input.providerId)!,
    [catalog.providers, input.providerId]
  )
  const selectedCredential = useMemo(
    () =>
      catalog.credentials.find((item) => item.id === input.credentialId) ??
      null,
    [catalog.credentials, input.credentialId]
  )
  const modelOptions = selectedCredential?.models.length
    ? selectedCredential.models
    : provider.models
  const runtimes = catalog.runtimes.filter(
    (item) => item.projectId === input.projectId
  )

  function update<K extends keyof AgentInput>(key: K, value: AgentInput[K]) {
    setInput((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: "" }))
    setFormError("")
  }

  function chooseProvider(providerId: string) {
    const next = catalog.providers.find((item) => item.id === providerId)!
    const credential = catalog.credentials.find(
      (item) => item.providerId === providerId && item.status === "configured"
    )
    const models = credential?.models.length ? credential.models : next.models
    setInput((current) => ({
      ...current,
      providerId,
      modelId:
        credential?.modelId &&
        models.some((model) => model.id === credential.modelId)
          ? credential.modelId
          : (models[0]?.id ?? ""),
      credentialId: credential?.id ?? null,
    }))
  }

  function chooseCredential(credentialId: string) {
    const credential = catalog.credentials.find(
      (item) => item.id === credentialId
    )
    const models = credential?.models.length
      ? credential.models
      : provider.models
    setInput((current) => ({
      ...current,
      credentialId,
      modelId:
        credential?.modelId &&
        models.some((model) => model.id === credential.modelId)
          ? credential.modelId
          : (models[0]?.id ?? ""),
    }))
  }

  function toggleList(
    key: "skillIds" | "mcpServerIds" | "variableIds",
    id: string,
    checked: boolean
  ) {
    update(
      key,
      checked ? [...input[key], id] : input[key].filter((item) => item !== id)
    )
  }

  async function submit() {
    const parsed = agentInputSchema.safeParse(input)
    if (!parsed.success) {
      setErrors(issuesToErrors(parsed.error.issues))
      setFormError("请检查表单中的必填项。")
      return
    }

    setSaving(true)
    setFormError("")
    try {
      await onSave(parsed.data, agent)
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "保存失败")
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !saving && onOpenChange(open)}>
      <DialogContent className="flex max-h-[92svh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
        <DialogHeader className="border-b px-5 py-4 sm:px-6">
          <DialogTitle>
            {agent ? `编辑 ${agent.name}` : "创建 Agent"}
          </DialogTitle>
          <DialogDescription>
            先填写角色和运行方式。保存 Agent 不会立即创建 Sandbox。
          </DialogDescription>
        </DialogHeader>

        <form
          className="min-h-0 flex-1 overflow-y-auto"
          onSubmit={(event) => {
            event.preventDefault()
            void submit()
          }}
        >
          <div className="flex flex-col gap-8 px-5 py-6 sm:px-6">
            {formError && (
              <Alert variant="destructive">
                <BotIcon />
                <AlertTitle>无法保存</AlertTitle>
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            )}

            <FieldGroup>
              <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_7rem]">
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
                  <FieldError>{errors.name}</FieldError>
                </Field>
                <Field data-invalid={Boolean(errors.avatar)}>
                  <FieldLabel htmlFor="agent-avatar">头像</FieldLabel>
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

              <Field data-invalid={Boolean(errors.description)}>
                <FieldLabel htmlFor="agent-description">简介</FieldLabel>
                <Textarea
                  id="agent-description"
                  value={input.description}
                  aria-invalid={Boolean(errors.description)}
                  placeholder="一句话说明这个 Agent 负责什么。"
                  rows={3}
                  onChange={(event) =>
                    update("description", event.target.value)
                  }
                />
                <FieldError>{errors.description}</FieldError>
              </Field>

              <Field data-invalid={Boolean(errors.systemPrompt)}>
                <FieldLabel htmlFor="agent-prompt">Instructions</FieldLabel>
                <Textarea
                  id="agent-prompt"
                  value={input.systemPrompt}
                  aria-invalid={Boolean(errors.systemPrompt)}
                  placeholder="说明角色、目标、工作方法、边界和输出要求。"
                  rows={8}
                  className="font-mono"
                  onChange={(event) =>
                    update("systemPrompt", event.target.value)
                  }
                />
                <FieldDescription>至少 20 个字符。</FieldDescription>
                <FieldError>{errors.systemPrompt}</FieldError>
              </Field>
            </FieldGroup>

            <FieldGroup>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Field data-invalid={Boolean(errors.runtimeId)}>
                  <FieldLabel>默认环境模板</FieldLabel>
                  <Select
                    value={input.runtimeId}
                    onValueChange={(value) => update("runtimeId", value)}
                  >
                    <SelectTrigger
                      className="w-full"
                      aria-invalid={Boolean(errors.runtimeId)}
                    >
                      <SelectValue placeholder="选择环境模板" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>当前 Project</SelectLabel>
                        {runtimes.map((runtime) => (
                          <SelectItem key={runtime.id} value={runtime.id}>
                            {runtime.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FieldDescription>当前只是运行环境声明。</FieldDescription>
                  <FieldError>{errors.runtimeId}</FieldError>
                </Field>

                <Field data-invalid={Boolean(errors.providerId)}>
                  <FieldLabel>Provider</FieldLabel>
                  <Select
                    value={input.providerId}
                    onValueChange={chooseProvider}
                  >
                    <SelectTrigger
                      className="w-full"
                      aria-invalid={Boolean(errors.providerId)}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>内置目录</SelectLabel>
                        {catalog.providers.map((item) => (
                          <SelectItem key={item.id} value={item.id}>
                            {item.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FieldError>{errors.providerId}</FieldError>
                </Field>

                <Field data-invalid={Boolean(errors.modelId)}>
                  <FieldLabel>Model</FieldLabel>
                  <Select
                    value={input.modelId}
                    onValueChange={(value) => update("modelId", value)}
                  >
                    <SelectTrigger
                      className="w-full"
                      aria-invalid={Boolean(errors.modelId)}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>{provider.name}</SelectLabel>
                        {modelOptions.map((model) => (
                          <SelectItem key={model.id} value={model.id}>
                            {model.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FieldError>{errors.modelId}</FieldError>
                </Field>

                <Field data-invalid={Boolean(errors.credentialId)}>
                  <FieldLabel>API Key</FieldLabel>
                  <Select
                    value={input.credentialId ?? ""}
                    onValueChange={chooseCredential}
                  >
                    <SelectTrigger
                      className="w-full"
                      aria-invalid={Boolean(errors.credentialId)}
                    >
                      <SelectValue placeholder="先配置 API Key" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>{provider.name}</SelectLabel>
                        {catalog.credentials
                          .filter(
                            (credential) =>
                              credential.providerId === provider.id
                          )
                          .map((credential) => (
                            <SelectItem
                              key={credential.id}
                              value={credential.id}
                              disabled={credential.status !== "configured"}
                            >
                              {credential.name}
                              {credential.status === "configured"
                                ? ""
                                : " · 待配置"}
                            </SelectItem>
                          ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FieldError>{errors.credentialId}</FieldError>
                </Field>
              </div>
            </FieldGroup>

            <FieldSet>
              <FieldLegend variant="label">Skills</FieldLegend>
              <FieldDescription>
                选择创建 Sandbox 时要注入的能力声明。
              </FieldDescription>
              <FieldGroup className="grid gap-3 sm:grid-cols-2">
                {catalog.skills.map((skill) => (
                  <Field
                    key={skill.id}
                    orientation="horizontal"
                    className="rounded-xl border p-3"
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
                        {skill.name}
                      </FieldLabel>
                      <FieldDescription>{skill.description}</FieldDescription>
                    </FieldContent>
                  </Field>
                ))}
              </FieldGroup>
            </FieldSet>

            <details className="rounded-xl border p-4">
              <summary className="cursor-pointer text-sm font-medium">
                高级配置
              </summary>
              <div className="mt-6 flex flex-col gap-8">
                <Field data-invalid={Boolean(errors.slug)}>
                  <FieldLabel htmlFor="agent-slug">唯一标识</FieldLabel>
                  <Input
                    id="agent-slug"
                    value={input.slug}
                    aria-invalid={Boolean(errors.slug)}
                    onChange={(event) => {
                      setSlugEdited(true)
                      update("slug", event.target.value.toLowerCase())
                    }}
                  />
                  <FieldError>{errors.slug}</FieldError>
                </Field>

                <FieldSet>
                  <FieldLegend variant="label">MCP Servers</FieldLegend>
                  <FieldDescription>
                    这里只保存绑定；当前目录不是实时连接探测结果。
                  </FieldDescription>
                  <FieldGroup className="grid gap-3 sm:grid-cols-2">
                    {catalog.mcpServers.map((server) => (
                      <Field
                        key={server.id}
                        orientation="horizontal"
                        className="rounded-xl border p-3"
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
                            {server.name}
                          </FieldLabel>
                          <FieldDescription>
                            {server.description}
                          </FieldDescription>
                        </FieldContent>
                      </Field>
                    ))}
                  </FieldGroup>
                </FieldSet>

                {catalog.variables.filter(
                  (item) => item.projectId === input.projectId
                ).length > 0 && (
                  <FieldSet>
                    <FieldLegend variant="label">Environment</FieldLegend>
                    <FieldGroup>
                      {catalog.variables
                        .filter((item) => item.projectId === input.projectId)
                        .map((variable) => (
                          <Field
                            key={variable.id}
                            orientation="horizontal"
                            className="rounded-xl border p-3"
                          >
                            <Checkbox
                              id={`variable-${variable.id}`}
                              checked={input.variableIds.includes(variable.id)}
                              onCheckedChange={(checked) =>
                                toggleList(
                                  "variableIds",
                                  variable.id,
                                  checked === true
                                )
                              }
                            />
                            <FieldContent>
                              <FieldLabel htmlFor={`variable-${variable.id}`}>
                                {String(variable.spec.key ?? variable.name)}
                              </FieldLabel>
                              <FieldDescription>
                                {String(
                                  variable.spec.reference ??
                                    variable.description
                                )}
                              </FieldDescription>
                            </FieldContent>
                          </Field>
                        ))}
                    </FieldGroup>
                  </FieldSet>
                )}

                <Field>
                  <FieldLabel htmlFor="agent-custom-args">
                    Custom args
                  </FieldLabel>
                  <Textarea
                    id="agent-custom-args"
                    value={input.customArgs.join("\n")}
                    rows={4}
                    className="font-mono"
                    placeholder="每行一个启动参数"
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
                </Field>

                <div className="grid gap-4 sm:grid-cols-2">
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
                          <SelectItem value="new">每次新建</SelectItem>
                          <SelectItem value="reuse">优先复用</SelectItem>
                          <SelectItem value="sticky">固定实例</SelectItem>
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
                          {[1, 2, 4, 8].map((value) => (
                            <SelectItem key={value} value={String(value)}>
                              {value}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </Field>
                </div>

                <Field>
                  <FieldLabel>保存状态</FieldLabel>
                  <ToggleGroup
                    type="single"
                    variant="outline"
                    value={input.status === "archived" ? "draft" : input.status}
                    onValueChange={(value) =>
                      value && update("status", value as "draft" | "active")
                    }
                  >
                    <ToggleGroupItem value="draft">草稿</ToggleGroupItem>
                    <ToggleGroupItem value="active">已声明</ToggleGroupItem>
                  </ToggleGroup>
                </Field>
              </div>
            </details>
          </div>

          <DialogFooter className="border-t px-5 py-4 sm:px-6">
            <Badge variant="outline">保存后仍需创建 Sandbox</Badge>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={saving}
            >
              取消
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <SaveIcon data-icon="inline-start" />
              )}
              {saving ? "保存中…" : agent ? "保存" : "创建并打开"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
