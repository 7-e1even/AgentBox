import { z } from "zod"

import { resourceInputSchema } from "./platform-schema"

export const automationAuthModeSchema = z.enum([
  "bearer",
  "hmac-sha256",
  "github-sha256",
  "gitlab-token",
  "standard-webhooks",
])

export const automationActionTypeSchema = z.enum([
  "create-sandbox",
  "run-task",
  "destroy-sandbox",
])

export const automationCleanupPolicySchema = z.enum([
  "never",
  "on-success",
  "always",
])

export const automationInputSchema = z
  .object({
    projectId: z.string().min(1),
    name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
    description: z.string().trim().max(500),
    enabled: z.boolean(),
    conditionTemplate: z
      .string()
      .min(1)
      .max(8 << 10)
      .default("true"),
    secret: z.string().min(16).max(512).optional(),
    trigger: z.object({
      type: z.literal("webhook"),
      authMode: automationAuthModeSchema,
    }),
    action: z.object({
      type: automationActionTypeSchema,
      templateId: z.string(),
      modelBindings: z.record(z.string(), z.string()),
      inputTemplate: z.string().max(64 << 10),
      targetTemplate: z
        .string()
        .max(8 << 10)
        .default(""),
      commandTemplate: z
        .string()
        .max(64 << 10)
        .default(""),
      timeoutSeconds: z.number().int().min(10).max(3600).default(900),
      cleanupPolicy: automationCleanupPolicySchema.default("never"),
      expiresAfterSeconds: z
        .number()
        .int()
        .min(0)
        .max(30 * 24 * 60 * 60)
        .default(0),
    }),
  })
  .superRefine((input, context) => {
    if (input.action.type !== "destroy-sandbox") {
      if (!input.action.templateId) {
        context.addIssue({
          code: "custom",
          path: ["action", "templateId"],
          message: "请选择沙箱模板",
        })
      }
      if (!input.action.inputTemplate) {
        context.addIssue({
          code: "custom",
          path: ["action", "inputTemplate"],
          message: "沙箱输入模板不能为空",
        })
      }
    }
    if (input.action.type === "run-task" && !input.action.commandTemplate) {
      context.addIssue({
        code: "custom",
        path: ["action", "commandTemplate"],
        message: "任务命令不能为空",
      })
    }
    if (
      input.action.type === "destroy-sandbox" &&
      !input.action.targetTemplate
    ) {
      context.addIssue({
        code: "custom",
        path: ["action", "targetTemplate"],
        message: "目标模板不能为空",
      })
    }
    if (
      input.action.expiresAfterSeconds > 0 &&
      input.action.expiresAfterSeconds < 60
    ) {
      context.addIssue({
        code: "custom",
        path: ["action", "expiresAfterSeconds"],
        message: "自动回收时间至少为 60 秒",
      })
    }
  })

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
  "running",
  "succeeded",
  "failed",
  "skipped",
  "expired",
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
  actionType: automationActionTypeSchema.default("create-sandbox"),
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
  exitCode: z.number().int().nullable().default(null),
  output: z.string().default(""),
  outputTruncated: z.boolean().default(false),
  cleanupStatus: z.string().default(""),
  errorCode: z.string(),
  errorMessage: z.string(),
  receivedAt: z.string().datetime({ offset: true }),
  queuedAt: z.string().datetime({ offset: true }).nullable(),
  startedAt: z.string().datetime({ offset: true }).nullable(),
  finishedAt: z.string().datetime({ offset: true }).nullable(),
  expiresAt: z.string().datetime({ offset: true }).nullable().default(null),
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

export const automationPreviewResponseSchema = z.object({
  matched: z.boolean(),
  command: z.string().optional(),
  target: z.string().optional(),
  input: resourceInputSchema.optional(),
})

export type AutomationAuthMode = z.infer<typeof automationAuthModeSchema>
export type AutomationActionType = z.infer<typeof automationActionTypeSchema>
export type AutomationInput = z.infer<typeof automationInputSchema>
export type Automation = z.infer<typeof automationSchema>
export type AutomationRun = z.infer<typeof automationRunSchema>
export type AutomationRunStatus = z.infer<typeof automationRunStatusSchema>
export type AutomationPreview = z.infer<typeof automationPreviewResponseSchema>
