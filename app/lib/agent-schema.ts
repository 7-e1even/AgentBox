import { z } from "zod"

export const agentStatusSchema = z.enum(["draft", "active", "archived"])

export const agentInputSchema = z.object({
  name: z
    .string()
    .trim()
    .min(2, "名称至少需要 2 个字符")
    .max(60, "名称不能超过 60 个字符"),
  slug: z
    .string()
    .trim()
    .min(2, "标识至少需要 2 个字符")
    .max(64, "标识不能超过 64 个字符")
    .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, "标识只能包含小写字母、数字和连字符"),
  description: z
    .string()
    .trim()
    .max(280, "简介不能超过 280 个字符")
    .default(""),
  avatar: z.string().trim().min(1).max(4),
  providerId: z.string().trim().min(1, "请选择 Provider"),
  modelId: z.string().trim().min(1, "请选择模型"),
  credentialId: z.string().trim().min(1).nullable(),
  systemPrompt: z
    .string()
    .trim()
    .min(20, "系统指令至少需要 20 个字符")
    .max(16000, "系统指令不能超过 16000 个字符"),
  skillIds: z.array(z.string()).max(20, "最多绑定 20 个 Skill"),
  mcpServerIds: z.array(z.string()).max(20, "最多绑定 20 个 MCP Server"),
  temperature: z.number().min(0).max(2),
  maxSteps: z.number().int().min(1).max(50),
  status: agentStatusSchema,
})

export const agentUpdateSchema = agentInputSchema.extend({
  version: z.number().int().positive(),
})

export const agentSchema = agentInputSchema.extend({
  id: z.string().uuid(),
  projectId: z.string().min(1),
  version: z.number().int().positive(),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

export const agentsResponseSchema = z.object({
  agents: z.array(agentSchema),
})

export const agentResponseSchema = z.object({
  agent: agentSchema,
})

export type AgentStatus = z.infer<typeof agentStatusSchema>
export type AgentInput = z.infer<typeof agentInputSchema>
export type Agent = z.infer<typeof agentSchema>

export function createSlug(value: string) {
  const slug = value
    .normalize("NFKD")
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9\s-]/g, "")
    .replace(/[\s_-]+/g, "-")
    .replace(/^-+|-+$/g, "")

  return slug || "agent"
}
