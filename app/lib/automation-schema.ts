import { z } from "zod"

export const automationAuthModeSchema = z.enum([
  "bearer",
  "hmac-sha256",
  "github-sha256",
  "gitlab-token",
  "standard-webhooks",
])

export const automationInputSchema = z
  .object({
    projectId: z.string().min(1),
    name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
    description: z.string().trim().max(500),
    enabled: z.boolean(),
    secret: z.string().min(16).max(512).optional(),
    trigger: z.object({
      type: z.literal("webhook"),
      authMode: automationAuthModeSchema,
    }),
    templateId: z.string().min(1, "请选择沙箱模板"),
  })
  .strict()

export const automationSchema = automationInputSchema.safeExtend({
  id: z.string().uuid(),
  endpointId: z.string().uuid(),
  secretLastFour: z.string(),
  createdBy: z.string().uuid().nullable(),
  updatedBy: z.string().uuid().nullable(),
  lastTriggeredAt: z.string().datetime({ offset: true }).nullable(),
  secretRotatedAt: z.string().datetime({ offset: true }),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

export const automationRunStatusSchema = z.enum([
  "evaluating",
  "queued",
  "provisioning",
  "succeeded",
  "failed",
])

export const automationEventSchema = z.object({
  id: z.string(),
  type: z.string(),
  source: z.string(),
  time: z.string().datetime({ offset: true }),
  receivedAt: z.string().datetime({ offset: true }),
})

export const automationRunSchema = z.object({
  id: z.string().uuid(),
  automationId: z.string().uuid().nullable(),
  endpointId: z.string().optional().default(""),
  projectId: z.string(),
  automationName: z.string(),
  templateId: z.string(),
  templateName: z.string(),
  triggerSource: z.enum(["webhook", "manual-test"]),
  authMode: automationAuthModeSchema,
  event: automationEventSchema.default({
    id: "",
    type: "event",
    source: "generic",
    time: "1970-01-01T00:00:00Z",
    receivedAt: "1970-01-01T00:00:00Z",
  }),
  idempotencyFingerprint: z.string(),
  payloadSha256: z.string(),
  payloadBytes: z.number().int().nonnegative(),
  inputSha256: z.string(),
  status: automationRunStatusSchema,
  sandboxId: z.string().nullable(),
  workerJobId: z.string().uuid().nullable(),
  errorCode: z.string(),
  errorMessage: z.string(),
  receivedAt: z.string().datetime({ offset: true }),
  queuedAt: z.string().datetime({ offset: true }).nullable(),
  startedAt: z.string().datetime({ offset: true }).nullable(),
  finishedAt: z.string().datetime({ offset: true }).nullable(),
})

export const automationsResponseSchema = z.object({
  automations: z.array(automationSchema),
})

export const automationResponseSchema = z.object({
  automation: automationSchema,
})

export const automationSecretResponseSchema = automationResponseSchema.extend({
  secret: z.string(),
  webhookPath: z.string(),
})

export const automationRunsResponseSchema = z.object({
  runs: z.array(automationRunSchema),
})

export const automationTriggerResponseSchema = z.object({
  run: automationRunSchema,
  duplicate: z.boolean(),
})

export type AutomationAuthMode = z.infer<typeof automationAuthModeSchema>
export type AutomationInput = z.infer<typeof automationInputSchema>
export type Automation = z.infer<typeof automationSchema>
export type AutomationRun = z.infer<typeof automationRunSchema>
export type AutomationRunStatus = z.infer<typeof automationRunStatusSchema>
