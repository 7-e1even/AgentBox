"use client"

import { useCallback, useRef } from "react"

import type {
  RuntimeImageChoice,
  RuntimeImageChoices,
} from "@/lib/runtime-images"
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox"

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
  const selectedOption =
    options.find((option) => option.value === value) ??
    (value ? { value, label: value } : null)
  const allowsRegistryReference = driver !== "vm"
  const portalContainerRef = useRef<HTMLElement | null>(null)
  const setInputElement = useCallback((input: HTMLInputElement | null) => {
    portalContainerRef.current =
      input?.closest<HTMLElement>('[data-slot="dialog-content"]') ?? null
  }, [])

  return (
    <Combobox<RuntimeImageChoice>
      items={options}
      value={selectedOption}
      inputValue={allowsRegistryReference ? value : undefined}
      disabled={disabled}
      autoHighlight
      itemToStringLabel={(option) => option.value}
      itemToStringValue={(option) => option.value}
      isItemEqualToValue={(option, selected) =>
        option.value === selected.value
      }
      onInputValueChange={(nextValue) => {
        if (allowsRegistryReference) onChange(nextValue)
      }}
      onValueChange={(option) => {
        if (!option) return
        onChange(option.value)
      }}
    >
      <ComboboxInput
        ref={setInputElement}
        id={id}
        className="w-full"
        placeholder={
          driver === "vm"
            ? "搜索本地 VM 镜像"
            : "搜索镜像，或输入 Registry 引用"
        }
        aria-invalid={invalid || undefined}
        autoComplete="off"
      />
      <ComboboxContent portalContainer={portalContainerRef}>
        <ComboboxEmpty>
          {allowsRegistryReference
            ? "没有匹配项，可直接输入新的 Registry 引用"
            : "没有匹配的本地 VM 镜像"}
        </ComboboxEmpty>
        <ComboboxList>
          {(option) => (
            <ComboboxItem key={option.value} value={option}>
              {option.label}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
