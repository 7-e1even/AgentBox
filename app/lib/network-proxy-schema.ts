import { z } from "zod"

const proxyIdSchema = z
  .string()
  .trim()
  .min(2, "标识至少需要 2 个字符")
  .max(64)
  .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, "标识只能包含小写字母、数字和连字符")

const networkProxyBaseSchema = z.object({
  id: proxyIdSchema,
  name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
  scheme: z.enum(["http", "https", "socks5", "socks5h"]),
  host: z
    .string()
    .trim()
    .min(1, "请填写代理主机")
    .max(253)
    .refine(
      (value) => !/[\s/@\\]/.test(value),
      "请输入主机名或 IP，不要包含协议或路径"
    ),
  port: z.number().int().min(1, "端口不能小于 1").max(65535),
  username: z.string().trim().max(512),
  password: z
    .string()
    .trim()
    .max(16 * 1024)
    .refine((value) => !/[\r\n]/.test(value), "密码不能包含换行符"),
  noProxy: z
    .array(
      z
        .string()
        .trim()
        .min(1)
        .max(255)
        .refine(
          (value) => value !== "*" && !/[\s,]/.test(value),
          "每行填写一个主机、IP 或 CIDR；不能使用全局 *"
        )
    )
    .max(100),
  enabled: z.boolean(),
})

export const networkProxyInputSchema = networkProxyBaseSchema.superRefine(
  (input, context) => {
    if (input.password && !input.username) {
      context.addIssue({
        code: "custom",
        path: ["username"],
        message: "填写密码时也需要填写用户名",
      })
    }
  }
)

export const managedNetworkProxySchema = networkProxyBaseSchema
  .omit({ password: true })
  .extend({
    maskedPassword: z.string(),
    hasPassword: z.boolean(),
    createdAt: z.string().datetime({ offset: true }),
    updatedAt: z.string().datetime({ offset: true }),
  })
  .strict()

export const networkProxiesResponseSchema = z.object({
  proxies: z.array(managedNetworkProxySchema),
})

export const networkProxyResponseSchema = z.object({
  proxy: managedNetworkProxySchema,
})

export const networkProxyCheckResultSchema = z
  .object({
    checkId: z.string().uuid(),
    proxyId: proxyIdSchema,
    serverId: z.string().uuid(),
    serverName: z.string().min(1),
    scope: z.literal("worker"),
    status: z.enum(["pending", "running", "completed"]),
    ok: z.boolean().optional(),
    latencyMs: z.number().int().nonnegative().optional(),
    target: z.string().url(),
    statusCode: z.number().int().min(100).max(599).optional(),
    error: z.string().optional(),
    checkedAt: z.string().datetime({ offset: true }).optional(),
  })
  .strict()
  .superRefine((result, context) => {
    if (result.status === "completed" && result.ok === undefined) {
      context.addIssue({
        code: "custom",
        path: ["ok"],
        message: "已完成的 Worker 检测必须包含结果",
      })
    }
  })

export const networkProxyCheckResponseSchema = z.object({
  result: networkProxyCheckResultSchema,
})

export const sandboxProxyOperationSchema = z
  .object({
    status: z.enum([
      "pending-start",
      "queued",
      "running",
      "succeeded",
      "failed",
    ]),
    desiredProxyId: z.string(),
    appliedProxyId: z.string(),
    message: z.string(),
    updatedAt: z.string().datetime({ offset: true }),
    finishedAt: z.string().datetime({ offset: true }).optional(),
  })
  .strict()

export type NetworkProxyInput = z.infer<typeof networkProxyInputSchema>
export type ManagedNetworkProxy = z.infer<typeof managedNetworkProxySchema>
export type NetworkProxyCheckResult = z.infer<
  typeof networkProxyCheckResultSchema
>
export type SandboxProxyOperation = z.infer<typeof sandboxProxyOperationSchema>
