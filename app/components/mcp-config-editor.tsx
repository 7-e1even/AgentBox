"use client"

import { PlusIcon, Trash2Icon, TriangleAlertIcon } from "lucide-react"

import {
  isMCPValueReference,
  mcpValueFromVariable,
  normalizeMCPArgs,
  normalizeMCPHeaders,
  unsafeLegacyMCPHeaderNames,
  type MCPHeaderReference,
} from "@/lib/mcp-config"
import type { ResourceOfKind } from "@/lib/platform-schema"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
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

type VariableOption = {
  id: string
  label: string
  valueFrom: string
}

export function MCPConfigEditor({
  spec,
  variables,
  legacyArgs,
  legacyHeaders,
  invalid,
  onChange,
}: {
  spec: Record<string, unknown>
  variables: ResourceOfKind<"variable">[]
  legacyArgs?: unknown
  legacyHeaders?: unknown
  invalid: boolean
  onChange: (key: string, value: unknown) => void
}) {
  const transport = spec.transport === "http" ? "http" : "stdio"
  const args = normalizeMCPArgs(spec.args)
  const headers = normalizeMCPHeaders(spec.headers)
  const unsafeHeaderNames = unsafeLegacyMCPHeaderNames(legacyHeaders)
  const variableOptions = availableVariables(variables)

  function updateArg(index: number, value: string) {
    onChange(
      "args",
      args.map((argument, argumentIndex) =>
        argumentIndex === index ? value : argument
      )
    )
  }

  function updateHeader(index: number, patch: Partial<MCPHeaderReference>) {
    onChange(
      "headers",
      headers.map((header, headerIndex) =>
        headerIndex === index ? { ...header, ...patch } : header
      )
    )
  }

  return (
    <FieldSet className="gap-4">
      <FieldLegend>MCP 连接</FieldLegend>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field data-invalid={invalid}>
          <FieldLabel htmlFor="mcp-transport">Transport</FieldLabel>
          <Select
            value={transport}
            onValueChange={(value) => onChange("transport", value)}
          >
            <SelectTrigger id="mcp-transport" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectLabel>连接方式</SelectLabel>
                <SelectItem value="stdio">stdio · 本地进程</SelectItem>
                <SelectItem value="http">HTTP · 远程服务</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>

        {transport === "stdio" ? (
          <Field data-invalid={invalid}>
            <FieldLabel htmlFor="mcp-command">启动程序</FieldLabel>
            <Input
              id="mcp-command"
              className="font-mono"
              value={stringValue(spec.command)}
              placeholder="npx"
              aria-invalid={invalid && !stringValue(spec.command)}
              onChange={(event) => onChange("command", event.target.value)}
            />
            <FieldDescription>
              只填写可执行程序，参数在下方逐项添加。
            </FieldDescription>
          </Field>
        ) : (
          <Field data-invalid={invalid}>
            <FieldLabel htmlFor="mcp-url">HTTP URL</FieldLabel>
            <Input
              id="mcp-url"
              type="url"
              className="font-mono"
              value={stringValue(spec.url)}
              placeholder="https://mcp.example.com"
              aria-invalid={invalid && !stringValue(spec.url)}
              onChange={(event) => onChange("url", event.target.value)}
            />
          </Field>
        )}
      </div>

      {transport === "stdio" ? (
        <>
          <Field>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <FieldLabel>参数</FieldLabel>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => onChange("args", [...args, ""])}
              >
                <PlusIcon data-icon="inline-start" />
                添加参数
              </Button>
            </div>
            {args.length > 0 ? (
              <div className="divide-y rounded-lg border">
                {args.map((argument, index) => (
                  <div key={index} className="flex items-center gap-2 p-2">
                    <span className="w-7 shrink-0 text-right font-mono text-xs text-muted-foreground">
                      {index + 1}
                    </span>
                    <Input
                      className="min-w-0 flex-1 font-mono"
                      value={argument}
                      aria-label={`第 ${index + 1} 个 MCP 参数`}
                      placeholder="--readonly"
                      onChange={(event) => updateArg(index, event.target.value)}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`删除第 ${index + 1} 个 MCP 参数`}
                      onClick={() =>
                        onChange(
                          "args",
                          args.filter(
                            (_, argumentIndex) => argumentIndex !== index
                          )
                        )
                      }
                    >
                      <Trash2Icon />
                    </Button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                当前命令不带参数。
              </p>
            )}
            {typeof legacyArgs === "string" && legacyArgs.trim() && (
              <FieldDescription>
                已将旧参数字符串拆成 {args.length} 项；保存后迁移为参数数组。
              </FieldDescription>
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor="mcp-cwd">工作目录（可选）</FieldLabel>
            <Input
              id="mcp-cwd"
              className="font-mono"
              value={stringValue(spec.cwd)}
              placeholder="/workspace"
              onChange={(event) => onChange("cwd", event.target.value)}
            />
          </Field>
        </>
      ) : (
        <Field>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <FieldLabel>请求 Headers</FieldLabel>
              <FieldDescription>
                值只能从当前项目的 Variable 引用，界面不会接收或保存明文密钥。
              </FieldDescription>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={variableOptions.length === 0}
              onClick={() =>
                onChange("headers", [
                  ...headers,
                  { name: "", valueFrom: variableOptions[0]?.valueFrom ?? "" },
                ])
              }
            >
              <PlusIcon data-icon="inline-start" />
              添加 Header
            </Button>
          </div>

          {unsafeHeaderNames.length > 0 && (
            <Alert variant="destructive">
              <TriangleAlertIcon />
              <AlertTitle>旧 Header 需要迁移</AlertTitle>
              <AlertDescription>
                {unsafeHeaderNames.join("、")}{" "}
                的旧值不是安全引用，已从编辑状态移除；请选择 Variable
                后才能保存。
              </AlertDescription>
            </Alert>
          )}

          {headers.length > 0 ? (
            <div className="divide-y rounded-lg border">
              {headers.map((header, index) => {
                const currentAvailable = variableOptions.some(
                  (option) => option.valueFrom === header.valueFrom
                )
                return (
                  <div
                    key={index}
                    className="grid gap-2 p-2 sm:grid-cols-[minmax(9rem,0.8fr)_minmax(13rem,1.2fr)_auto] sm:items-center"
                  >
                    <Input
                      className="font-mono"
                      value={header.name}
                      placeholder="Authorization"
                      aria-label={`第 ${index + 1} 个 Header 名称`}
                      aria-invalid={!header.name}
                      onChange={(event) =>
                        updateHeader(index, { name: event.target.value })
                      }
                    />
                    <Select
                      value={header.valueFrom || "__missing__"}
                      onValueChange={(value) =>
                        updateHeader(index, { valueFrom: value })
                      }
                    >
                      <SelectTrigger
                        className="w-full font-mono"
                        aria-label={`${header.name || `第 ${index + 1} 个 Header`}的值来源`}
                        aria-invalid={!header.valueFrom || !currentAvailable}
                      >
                        <SelectValue placeholder="选择 Variable" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectLabel>当前项目 Variables</SelectLabel>
                          {(!header.valueFrom || !currentAvailable) && (
                            <SelectItem value="__missing__" disabled>
                              {header.valueFrom
                                ? `${header.valueFrom} · 引用不可用`
                                : "请选择安全引用"}
                            </SelectItem>
                          )}
                          {variableOptions.map((option) => (
                            <SelectItem
                              key={option.id}
                              value={option.valueFrom}
                            >
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`删除 ${header.name || `第 ${index + 1} 个 Header`}`}
                      onClick={() =>
                        onChange(
                          "headers",
                          headers.filter(
                            (_, headerIndex) => headerIndex !== index
                          )
                        )
                      }
                    >
                      <Trash2Icon />
                    </Button>
                  </div>
                )
              })}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">
              {variableOptions.length > 0
                ? "当前请求不带自定义 Header。"
                : "当前项目没有可用的 env:// 或 secret:// Variable 引用。"}
            </p>
          )}
          <FieldDescription>
            在模板或沙箱中绑定这个 MCP 时，也要选择对应的 Variable，Worker
            才能解析引用。
          </FieldDescription>
        </Field>
      )}
    </FieldSet>
  )
}

function availableVariables(
  variables: ResourceOfKind<"variable">[]
): VariableOption[] {
  const seen = new Set<string>()
  return variables.flatMap((variable) => {
    const valueFrom = mcpValueFromVariable(variable.spec)
    if (
      !variable.enabled ||
      !isMCPValueReference(valueFrom) ||
      seen.has(valueFrom)
    ) {
      return []
    }
    seen.add(valueFrom)
    const key = stringValue(variable.spec.key) || variable.id
    return [
      {
        id: variable.id,
        valueFrom,
        label: `${variable.name} · ${key} → ${valueFrom.split(":", 1)[0]}`,
      },
    ]
  })
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : ""
}
