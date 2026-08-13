import { z } from "zod"

export const credentialInputSchema = z.object({
  id: z
    .string()
    .trim()
    .min(2, "标识至少需要 2 个字符")
    .max(64)
    .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, "标识只能包含小写字母、数字和连字符"),
  name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
  providerId: z.string().trim().min(1, "请选择 Agent Provider"),
  protocol: z.enum(["openai-responses", "openai-chat", "anthropic", "gemini"]),
  endpoint: z.union([
    z.literal(""),
    z.string().trim().url("请输入有效的接口地址").max(500),
  ]),
  modelId: z.string().trim().max(160),
  secret: z
    .string()
    .trim()
    .max(16 * 1024),
  enabled: z.boolean(),
})

export const credentialModelSchema = z.object({
  id: z.string(),
  name: z.string(),
  group: z.string(),
  source: z.enum(["remote", "manual"]),
})

export const managedCredentialSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    providerId: z.string(),
    protocol: z.enum([
      "openai-responses",
      "openai-chat",
      "anthropic",
      "gemini",
    ]),
    endpoint: z.string(),
    modelId: z.string(),
    models: z.array(credentialModelSchema),
    maskedSecret: z.string(),
    enabled: z.boolean(),
    lastCheckAt: z.string().datetime({ offset: true }).nullable(),
    lastCheckOk: z.boolean().nullable(),
    lastCheckError: z.string(),
    createdAt: z.string().datetime({ offset: true }),
    updatedAt: z.string().datetime({ offset: true }),
  })
  .strict()

export const credentialsResponseSchema = z.object({
  credentials: z.array(managedCredentialSchema),
})

export const credentialResponseSchema = z.object({
  credential: managedCredentialSchema,
})

export const credentialModelsResponseSchema = z.object({
  models: z.array(credentialModelSchema),
})

export type CredentialInput = z.infer<typeof credentialInputSchema>
export type ManagedCredential = z.infer<typeof managedCredentialSchema>
export type CredentialModel = z.infer<typeof credentialModelSchema>
