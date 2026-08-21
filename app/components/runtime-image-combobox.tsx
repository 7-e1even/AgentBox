"use client"

import type { RuntimeImageChoices } from "@/lib/runtime-images"
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox"
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

  const selectedOption =
    options.find((option) => option.value === value) ?? null

  return (
    <Combobox
      items={options}
      value={selectedOption}
      inputValue={value}
      disabled={disabled}
      itemToStringValue={(option) => option.value}
      onInputValueChange={(nextValue) => onChange(nextValue)}
      onValueChange={(option) => option && onChange(option.value)}
    >
      <ComboboxInput
        id={id}
        placeholder="搜索镜像，或输入 Registry 引用"
        disabled={disabled}
        aria-invalid={invalid || undefined}
        className="w-full"
      />
      <ComboboxContent>
        <ComboboxEmpty>
          没有匹配的缓存镜像，将在创建时按当前引用拉取
        </ComboboxEmpty>
        <ComboboxList className="[scrollbar-width:thin] [scrollbar-color:var(--border)_transparent] [scrollbar-gutter:stable] [&::-webkit-scrollbar]:w-2 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-border [&::-webkit-scrollbar-track]:bg-transparent">
          {(option) => {
            const local = choices.local.some(
              (item) => item.value === option.value
            )
            const detail = option.label.startsWith(option.value)
              ? option.label.slice(option.value.length).replace(/^\s*·\s*/, "")
              : option.label

            return (
              <ComboboxItem
                key={option.value}
                value={option}
                className="px-2.5 py-2 pr-8"
              >
                <span
                  className="min-w-0 flex-1 truncate font-mono text-xs"
                  title={option.value}
                >
                  {option.value}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {local
                    ? `已缓存${detail ? ` · ${detail}` : ""}`
                    : detail || "创建时拉取"}
                </span>
              </ComboboxItem>
            )
          }}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
