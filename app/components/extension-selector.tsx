"use client"

import { useId, useState } from "react"
import { AlertTriangleIcon, SearchIcon, XIcon } from "lucide-react"

import { CollectionPanel } from "@/components/collection-list"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Resource } from "@/lib/platform-schema"
import {
  extensionSelectionOptions,
  filterExtensionSelectionOptions,
  sandboxExtensions,
} from "@/lib/sandbox-extensions"

export function ExtensionSelector({
  resources,
  selectedIds,
  onChange,
  disabled = false,
  network,
}: {
  resources: Resource[]
  selectedIds: string[]
  onChange: (ids: string[]) => void
  disabled?: boolean
  network?: string
}) {
  const fieldId = useId()
  const [query, setQuery] = useState("")
  const [scope, setScope] = useState("all")
  const extensions = sandboxExtensions(resources)
  const selected = new Set(selectedIds)
  const options = extensionSelectionOptions(extensions, selectedIds)
  const visible = filterExtensionSelectionOptions(
    options,
    selectedIds,
    query,
    scope === "selected"
  )
  const unavailable = options.filter(
    ({ id, extension }) =>
      selected.has(id) && (!extension || !extension.enabled)
  )
  const needsNetwork = extensions.filter(
    (extension) => selected.has(extension.id) && extension.spec.requiresNetwork
  )

  return (
    <FieldSet>
      <FieldLegend variant="label">创建时安装的扩展</FieldLegend>
      <FieldDescription>
        {disabled
          ? "创建时选择已固定，启动或重启不会重新安装扩展。"
          : "按所选顺序在新建沙箱内安装并验证，最多选择 64 个。修改模板不影响已经创建的沙箱。"}
      </FieldDescription>
      <div className="flex flex-wrap items-center gap-2">
        <InputGroup className="min-w-40 flex-1">
          <InputGroupInput
            aria-label="搜索可选扩展"
            placeholder="搜索名称、标识或说明…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.preventDefault()
            }}
          />
          <InputGroupAddon>
            <SearchIcon />
          </InputGroupAddon>
          {query && (
            <InputGroupAddon align="inline-end">
              <InputGroupButton
                size="icon-xs"
                aria-label="清除扩展搜索"
                onClick={() => setQuery("")}
              >
                <XIcon />
              </InputGroupButton>
            </InputGroupAddon>
          )}
        </InputGroup>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={scope}
          onValueChange={(value) => value && setScope(value)}
          aria-label="扩展选择范围"
        >
          <ToggleGroupItem value="all">全部</ToggleGroupItem>
          <ToggleGroupItem value="selected">
            已选 {selected.size}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      <CollectionPanel>
        {visible.length ? (
          <FieldGroup className="max-h-64 overflow-y-auto p-3">
            {visible.map(({ id, extension }) => {
              const checked = selected.has(id)
              const unavailable = !extension || !extension.enabled
              const choiceDisabled =
                disabled || (!checked && (unavailable || selected.size >= 64))
              const checkboxId = `${fieldId}-${id}`
              return (
                <Field
                  key={id}
                  orientation="horizontal"
                  data-disabled={choiceDisabled}
                >
                  <Checkbox
                    id={checkboxId}
                    aria-describedby={`${checkboxId}-description`}
                    checked={checked}
                    disabled={choiceDisabled}
                    onCheckedChange={(value) =>
                      onChange(
                        value === true
                          ? [...new Set([...selectedIds, id])]
                          : selectedIds.filter(
                              (selectedId) => selectedId !== id
                            )
                      )
                    }
                  />
                  <FieldContent className="min-w-0">
                    <FieldLabel htmlFor={checkboxId} className="flex-wrap">
                      <span className="break-all">{extension?.name ?? id}</span>
                      {unavailable && (
                        <Badge variant="outline">
                          {extension ? "已停用" : "不可用"}
                        </Badge>
                      )}
                    </FieldLabel>
                    <FieldDescription
                      id={`${checkboxId}-description`}
                      className="break-words"
                    >
                      {extension
                        ? `${extension.spec.version || "未配置版本"} · ${extension.description || extension.id}`
                        : "该扩展已删除或不在当前项目中，请移除后重新选择。"}
                    </FieldDescription>
                  </FieldContent>
                </Field>
              )
            })}
          </FieldGroup>
        ) : (
          <Empty className="py-6">
            <EmptyHeader>
              <EmptyTitle>
                {scope === "selected" && !selected.size
                  ? "尚未选择扩展"
                  : options.length
                    ? "没有匹配的扩展"
                    : "暂无可用扩展"}
              </EmptyTitle>
              <EmptyDescription>
                {options.length
                  ? "调整搜索条件，或切换到全部扩展。"
                  : "先到「沙箱扩展」创建扩展，再在这里选择。"}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
      </CollectionPanel>
      {!disabled && unavailable.length > 0 && (
        <Alert>
          <AlertTriangleIcon />
          <AlertTitle>所选扩展包含不可用项</AlertTitle>
          <AlertDescription>
            {unavailable
              .map(({ id, extension }) => extension?.name ?? id)
              .join("、")}
            。选择已保留，请移除或重新启用后再创建沙箱。
          </AlertDescription>
        </Alert>
      )}
      {!disabled && network === "none" && needsNetwork.length > 0 && (
        <Alert>
          <AlertTriangleIcon />
          <AlertTitle>所选扩展需要网络</AlertTitle>
          <AlertDescription>
            {needsNetwork.map((extension) => extension.name).join("、")}{" "}
            需要网络，当前沙箱完全隔离。请调整网络策略或移除这些扩展。
          </AlertDescription>
        </Alert>
      )}
    </FieldSet>
  )
}
