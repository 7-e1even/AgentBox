"use client"

import type { RuntimeImageChoices } from "@/lib/runtime-images"
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

export function RuntimeImageCombobox({
  id,
  value,
  choices,
  driver,
  disabled,
  invalid,
  onChange,
}: {
  id: string
  value: string
  choices: RuntimeImageChoices
  driver: string
  disabled?: boolean
  invalid?: boolean
  onChange: (value: string) => void
}) {
  const options = [...choices.local, ...choices.registry]

  if (driver === "vm") {
    return (
      <Select
        value={value}
        disabled={disabled || options.length === 0}
        onValueChange={onChange}
      >
        <SelectTrigger
          id={id}
          className="w-full"
          aria-invalid={invalid || undefined}
        >
          <SelectValue
            placeholder={
              options.length === 0
                ? "没有可用的本地 VM 镜像"
                : "选择本地 VM 镜像"
            }
          />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectLabel>Worker 本地 VM 镜像</SelectLabel>
            {options.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    )
  }

  const listId = `${id}-choices`

  return (
    <>
      <Input
        id={id}
        list={options.length > 0 ? listId : undefined}
        value={value}
        placeholder="搜索镜像，或输入 Registry 引用"
        disabled={disabled}
        aria-invalid={invalid || undefined}
        autoComplete="off"
        onChange={(event) => onChange(event.target.value)}
      />
      {options.length > 0 ? (
        <datalist id={listId}>
          {options.map((option) => (
            <option
              key={option.value}
              value={option.value}
              label={option.label}
            />
          ))}
        </datalist>
      ) : null}
    </>
  )
}
