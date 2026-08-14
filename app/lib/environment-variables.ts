export type EnvironmentVariableEntry = {
  name: string
  value: string
}

const environmentVariableNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/

export function environmentVariableEntries(
  value: unknown
): EnvironmentVariableEntry[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((entry) => {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) return []
    const candidate = entry as Record<string, unknown>
    if (typeof candidate.name !== "string") return []
    return [
      {
        name: candidate.name,
        value: typeof candidate.value === "string" ? candidate.value : "",
      },
    ]
  })
}

export function environmentVariablesError(value: unknown) {
  const entries = environmentVariableEntries(value)
  if (entries.length > 100) return "环境变量不能超过 100 个"

  const names = new Set<string>()
  for (const entry of entries) {
    if (!entry.name) return "请填写环境变量名"
    if (!environmentVariableNamePattern.test(entry.name)) {
      return `环境变量名 ${entry.name} 格式不正确`
    }
    if (entry.name.startsWith("AGENTBOX_")) {
      return "AGENTBOX_ 前缀由平台保留"
    }
    if (names.has(entry.name)) return `环境变量 ${entry.name} 重复`
    if (entry.value.length > 16 * 1024) {
      return `环境变量 ${entry.name} 的值过长`
    }
    if (entry.value.includes("\n") || entry.value.includes("\r")) {
      return `环境变量 ${entry.name} 的值不能换行`
    }
    names.add(entry.name)
  }
  return ""
}
