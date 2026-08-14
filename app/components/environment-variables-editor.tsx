"use client"

import { PlusIcon, Trash2Icon } from "lucide-react"

import {
  environmentVariableEntries,
  type EnvironmentVariableEntry,
} from "@/lib/environment-variables"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function EnvironmentVariablesEditor({
  value,
  onChange,
}: {
  value: unknown
  onChange: (value: EnvironmentVariableEntry[]) => void
}) {
  const entries = environmentVariableEntries(value)

  function update(index: number, patch: Partial<EnvironmentVariableEntry>) {
    onChange(
      entries.map((entry, entryIndex) =>
        entryIndex === index ? { ...entry, ...patch } : entry
      )
    )
  }

  function remove(index: number) {
    onChange(entries.filter((_, entryIndex) => entryIndex !== index))
  }

  return (
    <div className="flex flex-col gap-2">
      {entries.length > 0 && (
        <div className="divide-y rounded-lg border">
          {entries.map((entry, index) => (
            <div key={index} className="flex items-start gap-2 p-2">
              <div className="grid min-w-0 flex-1 gap-2 sm:grid-cols-[minmax(9rem,0.8fr)_minmax(12rem,1.2fr)]">
                <Input
                  aria-label={`第 ${index + 1} 个环境变量名`}
                  className="font-mono"
                  value={entry.name}
                  placeholder="NODE_ENV"
                  onChange={(event) =>
                    update(index, { name: event.target.value })
                  }
                />
                <Input
                  aria-label={`${entry.name || `第 ${index + 1} 个环境变量`}的值`}
                  className="font-mono"
                  value={entry.value}
                  placeholder="production"
                  onChange={(event) =>
                    update(index, { value: event.target.value })
                  }
                />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={`删除 ${entry.name || `第 ${index + 1} 个环境变量`}`}
                onClick={() => remove(index)}
              >
                <Trash2Icon />
              </Button>
            </div>
          ))}
        </div>
      )}
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          普通配置会明文保存；API Key 请使用模型服务。
        </p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...entries, { name: "", value: "" }])}
        >
          <PlusIcon data-icon="inline-start" />
          添加变量
        </Button>
      </div>
    </div>
  )
}
