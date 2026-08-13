"use client"

import { useState } from "react"
import { SaveIcon } from "lucide-react"

import { createSlug, type Agent } from "@/lib/agent-schema"
import {
  resourceInputSchema,
  type Resource,
  type ResourceInput,
  type ResourceKind,
} from "@/lib/platform-schema"
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

const labels: Record<ResourceKind, string> = {
  project: "项目",
  runtime: "Runtime 模板",
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
}

function fields(
  kind: ResourceKind,
  agents: Agent[],
  resources: Resource[]
): SpecField[] {
  const agentOptions = agents.map((item) => ({
    value: item.id,
    label: item.name,
  }))
  const runtimeOptions = resources
    .filter((item) => item.kind === "runtime")
    .map((item) => ({ value: item.id, label: item.name }))
  switch (kind) {
    case "project":
      return [
        {
          key: "source",
          label: "工作区来源",
          options: values("git", "local", "empty"),
        },
        {
          key: "repository",
          label: "仓库地址",
          placeholder: "https://git.example.com/team/repo.git",
        },
        { key: "branch", label: "默认分支", placeholder: "main" },
        { key: "path", label: "本地工作区", placeholder: "/workspace/project" },
      ]
    case "runtime":
      return [
        {
          key: "driver",
          label: "隔离驱动",
          options: values("process", "docker", "boxlite", "microsandbox"),
        },
        { key: "image", label: "容器镜像", placeholder: "ubuntu:24.04" },
        {
          key: "base",
          label: "主机基础环境",
          placeholder: "Python 3.12 / Node.js 22",
        },
        { key: "workdir", label: "工作目录", placeholder: "/workspace" },
        {
          key: "setup",
          label: "初始化命令",
          placeholder: "python -m venv .venv",
          textarea: true,
        },
        { key: "cpu", label: "CPU", placeholder: "2" },
        { key: "memory", label: "内存", placeholder: "4 GiB" },
        {
          key: "network",
          label: "网络策略",
          options: values("none", "restricted", "egress"),
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
        { key: "runtimeId", label: "Runtime", options: runtimeOptions },
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
  const common: Record<ResourceKind, Record<string, string>> = {
    project: { source: "git", branch: "main" },
    runtime: {
      driver: "docker",
      workdir: "/workspace",
      cpu: "2",
      memory: "4 GiB",
      network: "restricted",
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

export function ResourceEditorDialog({
  kind,
  resource,
  projectId,
  resources,
  agents,
  onOpenChange,
  onSave,
}: {
  kind: ResourceKind
  resource: Resource | null
  projectId: string
  resources: Resource[]
  agents: Agent[]
  onOpenChange: (open: boolean) => void
  onSave: (input: ResourceInput) => Promise<void>
}) {
  const [input, setInput] = useState<ResourceInput>(() =>
    resource
      ? resourceInputSchema.parse(resource)
      : {
          id: "",
          kind,
          projectId: kind === "project" ? null : projectId,
          name: "",
          description: "",
          enabled: true,
          spec: defaults(kind),
        }
  )
  const [slugEdited, setSlugEdited] = useState(Boolean(resource))
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const projects = resources.filter((item) => item.kind === "project")

  function update<K extends keyof ResourceInput>(
    key: K,
    value: ResourceInput[K]
  ) {
    setInput((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: "" }))
  }

  function updateSpec(key: string, value: string) {
    setInput((current) => ({
      ...current,
      spec: { ...current.spec, [key]: value },
    }))
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
            配置会保存在平台控制面，Runtime worker 只消费已启用的声明。
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          {kind !== "project" && (
            <Field>
              <FieldLabel>所属项目</FieldLabel>
              <Select
                value={input.projectId ?? ""}
                onValueChange={(value) => update("projectId", value)}
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
            {fields(kind, agents, resources).map((field) => (
              <Field
                key={field.key}
                className={field.textarea ? "sm:col-span-2" : undefined}
                data-invalid={Boolean(errors.spec)}
              >
                <FieldLabel htmlFor={`spec-${field.key}`}>
                  {field.label}
                </FieldLabel>
                {field.options ? (
                  <Select
                    value={String(input.spec[field.key] ?? "")}
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
                {field.description && (
                  <FieldDescription>{field.description}</FieldDescription>
                )}
              </Field>
            ))}
          </div>
          <Field orientation="horizontal" className="rounded-lg border p-3">
            <Checkbox
              id="resource-enabled"
              checked={input.enabled}
              onCheckedChange={(checked) => update("enabled", checked === true)}
            />
            <div>
              <FieldLabel htmlFor="resource-enabled">启用配置</FieldLabel>
              <FieldDescription>
                禁用后保留声明，但不会交给 Runtime worker。
              </FieldDescription>
            </div>
          </Field>
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
