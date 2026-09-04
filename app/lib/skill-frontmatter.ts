export type SkillFrontmatter = {
  name: string
  description: string
  license: string
  compatibility: string
}

export type SkillFrontmatterResult = {
  metadata: SkillFrontmatter
  issues: string[]
}

const emptyMetadata: SkillFrontmatter = {
  name: "",
  description: "",
  license: "",
  compatibility: "",
}

export function readSkillFrontmatter(document: string): SkillFrontmatterResult {
  const normalized = document.replace(/^\uFEFF/, "").replaceAll("\r\n", "\n")
  const lines = normalized.split("\n")
  if (!normalized.startsWith("---\n")) {
    return {
      metadata: emptyMetadata,
      issues: ["SKILL.md 必须以 YAML frontmatter（---）开头"],
    }
  }
  const end = lines.findIndex((line, index) => index > 0 && line === "---")
  if (end < 0) {
    return {
      metadata: emptyMetadata,
      issues: ["SKILL.md 缺少 frontmatter 结束标记（---）"],
    }
  }
  if (
    new TextEncoder().encode(lines.slice(1, end).join("\n")).length >
    16 * 1024
  ) {
    return {
      metadata: emptyMetadata,
      issues: ["SKILL.md 的 YAML frontmatter 不能超过 16 KiB"],
    }
  }

  const metadata = { ...emptyMetadata }
  const issues: string[] = []
  const seen = new Set<string>()
  const keys = new Set<keyof SkillFrontmatter>([
    "name",
    "description",
    "license",
    "compatibility",
  ])

  for (let index = 1; index < end; index += 1) {
    const match = lines[index]?.match(/^([A-Za-z][A-Za-z0-9_-]*):(?:\s*(.*))?$/)
    if (!match || !keys.has(match[1] as keyof SkillFrontmatter)) continue
    const key = match[1] as keyof SkillFrontmatter
    if (seen.has(key)) {
      issues.push(`frontmatter 的 ${key} 不能重复`)
      continue
    }
    seen.add(key)
    const raw = match[2] ?? ""
    if (
      raw === "|" ||
      raw === ">" ||
      raw.startsWith("|-") ||
      raw.startsWith(">-")
    ) {
      const block: string[] = []
      while (index + 1 < end && /^\s+/.test(lines[index + 1] ?? "")) {
        block.push((lines[index + 1] ?? "").replace(/^\s+/, ""))
        index += 1
      }
      metadata[key] = raw.startsWith(">")
        ? block.join(" ").trim()
        : block.join("\n").trim()
      continue
    }
    metadata[key] = decodeYAMLScalar(raw)
  }

  if (!metadata.name) issues.push("frontmatter 必须包含非空 name")
  else if (
    metadata.name.length > 64 ||
    !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(metadata.name)
  ) {
    issues.push(
      "frontmatter name 只能包含小写字母、数字和连字符，且不超过 64 个字符"
    )
  }
  if (!metadata.description) issues.push("frontmatter 必须包含非空 description")
  else if (Array.from(metadata.description).length > 1024)
    issues.push("frontmatter description 不能超过 1024 个字符")
  if (
    Array.from(metadata.license).length > 256 ||
    /[\0\r\n]/.test(metadata.license)
  ) {
    issues.push("frontmatter license 不能超过 256 个字符或包含换行")
  }
  if (
    Array.from(metadata.compatibility).length > 500 ||
    metadata.compatibility.includes("\0")
  ) {
    issues.push("frontmatter compatibility 不能超过 500 个字符或包含空字节")
  }
  return { metadata, issues }
}

export function skillDocumentIssue(document: string, resourceId: string) {
  const result = readSkillFrontmatter(document)
  if (result.issues.length > 0) return result.issues[0]
  if (result.metadata.name !== resourceId) {
    return `frontmatter name 必须与唯一标识一致（${resourceId || "请先填写唯一标识"}）`
  }
  const body = skillDocumentBody(document)
  if (!body.trim()) return "SKILL.md 必须包含 frontmatter 之后的指令正文"
  return ""
}

export function syncSkillDocument(
  document: string,
  values: { name: string; description: string }
) {
  const normalized = document.replace(/^\uFEFF/, "").replaceAll("\r\n", "\n")
  const lines = normalized.split("\n")
  const end = normalized.startsWith("---\n")
    ? lines.findIndex((line, index) => index > 0 && line === "---")
    : -1
  if (end < 0) {
    const body = normalized.trim()
    return [
      "---",
      `name: ${yamlString(values.name)}`,
      `description: ${yamlString(values.description)}`,
      "---",
      body,
      "",
    ].join("\n")
  }

  const header = lines.slice(1, end)
  const body = lines.slice(end + 1).join("\n")
  const nextHeader = setHeaderValue(
    setHeaderValue(header, "name", values.name),
    "description",
    values.description
  )
  return ["---", ...nextHeader, "---", body].join("\n")
}

export function skillDocumentBody(document: string) {
  const normalized = document.replace(/^\uFEFF/, "").replaceAll("\r\n", "\n")
  const lines = normalized.split("\n")
  if (!normalized.startsWith("---\n")) return normalized
  const end = lines.findIndex((line, index) => index > 0 && line === "---")
  return end < 0 ? "" : lines.slice(end + 1).join("\n")
}

function setHeaderValue(lines: string[], key: string, value: string) {
  const next = [...lines]
  const index = next.findIndex((line) =>
    new RegExp(`^${escapeRegExp(key)}\\s*:`).test(line)
  )
  const replacement = `${key}: ${yamlString(value)}`
  if (index < 0) return [...next, replacement]

  let deleteCount = 1
  const current = next[index] ?? ""
  if (/:[ \t]*[|>][-+]?\s*$/.test(current)) {
    while (
      index + deleteCount < next.length &&
      /^\s+/.test(next[index + deleteCount] ?? "")
    ) {
      deleteCount += 1
    }
  }
  next.splice(index, deleteCount, replacement)
  return next
}

function decodeYAMLScalar(raw: string) {
  const value = raw.trim()
  if (value.startsWith('"') && value.endsWith('"')) {
    try {
      return JSON.parse(value) as string
    } catch {
      return value.slice(1, -1)
    }
  }
  if (value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replaceAll("''", "'")
  }
  return value.replace(/\s+#.*$/, "").trim()
}

function yamlString(value: string) {
  return JSON.stringify(value)
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}
