import { z } from "zod"

export const providerModelSchema = z.object({
  id: z.string(),
  name: z.string(),
  context: z.string(),
  note: z.string(),
})

export const providerSchema = z.object({
  id: z.string(),
  name: z.string(),
  mark: z.string(),
  description: z.string(),
  models: z.array(providerModelSchema),
})

export const credentialSchema = z.object({
  id: z.string(),
  name: z.string(),
  providerId: z.string(),
  environment: z.string(),
  status: z.enum(["configured", "attention"]),
  modelId: z.string().default(""),
  models: z.array(z.object({ id: z.string(), name: z.string() })).default([]),
})

export const skillDefinitionSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  version: z.string(),
  category: z.string(),
})

export const mcpServerDefinitionSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  transport: z.enum(["stdio", "http"]),
  toolCount: z.number().int().nonnegative(),
  status: z.enum(["ready", "attention"]),
})

export const catalogSchema = z.object({
  project: z.object({ id: z.string(), name: z.string() }),
  providers: z.array(providerSchema),
  credentials: z.array(credentialSchema),
  skills: z.array(skillDefinitionSchema),
  mcpServers: z.array(mcpServerDefinitionSchema),
})

export type ProviderModel = z.infer<typeof providerModelSchema>
export type Provider = z.infer<typeof providerSchema>
export type Credential = z.infer<typeof credentialSchema>
export type SkillDefinition = z.infer<typeof skillDefinitionSchema>
export type McpServerDefinition = z.infer<typeof mcpServerDefinitionSchema>
export type AgentCatalog = z.infer<typeof catalogSchema>
