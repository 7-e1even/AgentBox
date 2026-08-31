import { z } from "zod"

import { skillSpecSchema } from "./platform-schema"

export const maxSkillUploadBytes = 4 * 1024 * 1024

export const skillImportResponseSchema = z.object({
  skill: z.object({
    name: z.string(),
    description: z.string(),
    spec: skillSpecSchema,
  }),
})

export type ImportedSkill = z.infer<typeof skillImportResponseSchema>["skill"]

export const skillSearchResponseSchema = z.object({
  query: z.string(),
  skills: z
    .array(
      z.object({
        id: z.string(),
        name: z.string(),
        source: z.string(),
        url: z.url(),
        installs: z.number().int().nonnegative(),
      })
    )
    .max(20),
  excluded: z.number().int().nonnegative(),
})

export type SkillSearchResult = z.infer<typeof skillSearchResponseSchema>

export function skillCatalogId(source: string) {
  try {
    const url = new URL(source)
    if (
      url.protocol !== "https:" ||
      !["skills.sh", "www.skills.sh"].includes(url.hostname)
    )
      return null
    const parts = decodeURIComponent(url.pathname).split("/").filter(Boolean)
    return parts.length === 3 ? parts.join("/").toLowerCase() : null
  } catch {
    return null
  }
}

export function skillUploadError(file: { name: string; size: number }) {
  if (!/\.(md|zip)$/i.test(file.name)) return "请选择 SKILL.md 或 ZIP 文件"
  if (file.size === 0) return "文件内容不能为空"
  if (file.size > maxSkillUploadBytes) return "文件不能超过 4 MiB"
  return ""
}
