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

export const catalogSchema = z.object({
  providers: z.array(providerSchema),
})

export type ProviderModel = z.infer<typeof providerModelSchema>
export type Provider = z.infer<typeof providerSchema>
export type AgentCatalog = z.infer<typeof catalogSchema>
