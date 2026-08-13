import { z } from "zod"

export const resourceKindSchema = z.enum([
  "project",
  "runtime",
  "skill",
  "mcp",
  "sandbox",
  "schedule",
  "webhook",
  "variable",
])

export const resourceInputSchema = z.object({
  id: z
    .string()
    .trim()
    .min(2, "标识至少需要 2 个字符")
    .max(64)
    .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, "标识只能包含小写字母、数字和连字符"),
  kind: resourceKindSchema,
  projectId: z.string().min(1).nullable(),
  name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
  description: z.string().trim().max(500),
  enabled: z.boolean(),
  spec: z.record(z.string(), z.unknown()),
})

export const resourceSchema = resourceInputSchema.extend({
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

export const resourcesResponseSchema = z.object({
  resources: z.array(resourceSchema),
})

export const resourceResponseSchema = z.object({ resource: resourceSchema })

export type ResourceKind = z.infer<typeof resourceKindSchema>
export type ResourceInput = z.infer<typeof resourceInputSchema>
export type Resource = z.infer<typeof resourceSchema>
