import { z } from "zod"

import { supportedAgentToolIds } from "./agent-tools"
import { sandboxProxyOperationSchema } from "./network-proxy-schema"

export const provisioningStageTimingSchema = z.object({
  stage: z.string(),
  startedAt: z.string().datetime({ offset: true }),
  finishedAt: z.string().datetime({ offset: true }),
  durationMs: z.number().int().nonnegative(),
})

export const provisioningAgentToolSchema = z.object({
  tool: z.string(),
  status: z.string(),
  message: z.string().default(""),
  startedAt: z.string().datetime({ offset: true }).nullable().default(null),
  finishedAt: z.string().datetime({ offset: true }).nullable().default(null),
  durationMs: z.number().int().nonnegative().default(0),
})

export const provisioningExtensionSchema = z.object({
  id: z.string(),
  status: z.string(),
  message: z.string().default(""),
  output: z.string().default(""),
  startedAt: z.string().datetime({ offset: true }).nullable().optional(),
  finishedAt: z.string().datetime({ offset: true }).nullable().optional(),
  durationMs: z.number().int().nonnegative().default(0),
})

export const extensionSpecSchema = z.object({
  version: z
    .string()
    .trim()
    .refine((value) => Array.from(value).length <= 64, "版本不能超过 64 个字符")
    .refine((value) => !/[\r\n\0]/.test(value), "版本不能包含换行或空字节")
    .default(""),
  source: z.enum(["custom", "preset"]).default("custom"),
  installScript: z
    .string()
    .refine(
      (value) =>
        new TextEncoder().encode(value).length <= 65536 &&
        !value.includes("\0"),
      "安装脚本不能超过 64 KiB 或包含空字节"
    )
    .default(""),
  verifyScript: z
    .string()
    .refine(
      (value) =>
        new TextEncoder().encode(value).length <= 65536 &&
        !value.includes("\0"),
      "验证脚本不能超过 64 KiB 或包含空字节"
    )
    .default(""),
  timeoutSeconds: z.number().int().min(30).max(1800).default(600),
  requiresNetwork: z.boolean().default(true),
})

export const extensionSnapshotSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  generation: z.number().int().positive(),
  spec: extensionSpecSchema,
})

export const provisioningProgressSchema = z.object({
  stage: z.string().default(""),
  message: z.string().default(""),
  status: z.string().default(""),
  cancellationSupported: z.boolean().default(false),
  cancelRequested: z.boolean().default(false),
  cacheStatus: z.string().default(""),
  cacheReason: z.string().default(""),
  startedAt: z.string().datetime({ offset: true }).nullable().default(null),
  stageStartedAt: z
    .string()
    .datetime({ offset: true })
    .nullable()
    .default(null),
  updatedAt: z.string().datetime({ offset: true }).nullable().default(null),
  finishedAt: z.string().datetime({ offset: true }).nullable().default(null),
  durationMs: z.number().int().nonnegative().default(0),
  timings: z.array(provisioningStageTimingSchema).default([]),
  agentTools: z.array(provisioningAgentToolSchema).default([]),
  extensions: z.array(provisioningExtensionSchema).default([]),
})

export const resourceKindSchema = z.enum([
  "project",
  "image",
  "runtime",
  "skill",
  "mcp",
  "sandbox",
  "variable",
  "extension",
])

const resourceBaseSchema = z.object({
  id: z
    .string()
    .trim()
    .min(2, "标识至少需要 2 个字符")
    .max(64)
    .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, "标识只能包含小写字母、数字和连字符"),
  projectId: z.string().min(1).nullable(),
  name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
  description: z.string().trim().max(500),
  enabled: z.boolean(),
  specVersion: z.literal(1).default(1),
})

const projectSpecSchema = z.object({ emoji: z.string().optional() })
const imageSpecSchema = z.object({
  reference: z.string(),
  architecture: z.string(),
  modes: z.array(z.string()),
})
const executionSpecSchema = z.object({
  serverId: z.string().optional(),
  driver: z.string().optional(),
  imageReference: z.string().optional(),
  imageId: z.string().optional(),
  workdir: z.string().optional(),
  setup: z.string().optional(),
  extensionIds: z.array(z.string().min(1)).max(64).optional(),
  cpu: z.string().optional(),
  memory: z.string().optional(),
  desktop: z.boolean().optional(),
  network: z.string().optional(),
  proxyId: z.string().optional(),
  agentTools: z.array(z.string()).optional(),
  skillIds: z.array(z.string()).optional(),
  mcpServerIds: z.array(z.string()).optional(),
  variableIds: z.array(z.string()).optional(),
  credentialIds: z.array(z.string()).optional(),
  environmentVariables: z
    .array(z.object({ name: z.string(), value: z.string() }))
    .optional(),
  modelBindings: z.record(z.string(), z.string()).optional(),
  workspace: z.string().optional(),
})
export const skillSpecSchema = z.object({
  version: z.string().optional(),
  category: z.string().optional(),
  source: z.string().optional(),
  path: z.string().optional(),
  instructions: z.string().optional(),
  files: z
    .array(
      z.object({
        path: z.string(),
        content: z.string(),
        executable: z.boolean().optional(),
      })
    )
    .optional(),
})
const mcpSpecSchema = z.object({
  transport: z.string(),
  command: z.string().optional(),
  args: z.string().optional(),
  url: z.string().optional(),
  headers: z.string().optional(),
})
const variableSpecSchema = z.object({
  key: z.string(),
  mode: z.string().optional(),
  reference: z.string(),
})

const executionInputSpecSchema = executionSpecSchema
  .extend({
    serverId: z.string().uuid("请选择运行服务器"),
    driver: z.enum(["docker", "boxlite", "microsandbox", "vm"]).optional(),
    network: z.enum(["", "none", "restricted", "egress"]).optional(),
    agentTools: z.array(z.enum(supportedAgentToolIds)).optional(),
  })
  .strict()

// These fields describe Worker observations/provenance, never desired input.
export const runtimeModelSourceSchema = z.object({
  credentialId: z.string().min(1),
  modelId: z.string().min(1),
  updatedAt: z.string().datetime({ offset: true }),
})

export const runtimeModelSourcesSchema = z.record(
  z.string().min(1),
  runtimeModelSourceSchema
)

const sandboxObservedSpecSchema = z.object({
  status: z.string().nullish(),
  message: z.string().nullish(),
  externalId: z.string().nullish(),
  provisioning: provisioningProgressSchema.nullish(),
  appliedProxyId: z.string().nullish(),
  proxyOperation: sandboxProxyOperationSchema.nullish(),
  agentToolVersions: z
    .array(
      z.object({
        tool: z.string(),
        currentVersion: z.string().optional(),
        latestVersion: z.string().optional(),
        previousVersion: z.string().optional(),
        status: z.string(),
        message: z.string().optional(),
        source: z.string().optional(),
        checkedAt: z.string().optional(),
      })
    )
    .nullish(),
  agentToolOperation: z
    .object({
      action: z.string(),
      status: z.string(),
      toolIds: z.array(z.string()).optional(),
      message: z.string().optional(),
      progress: provisioningProgressSchema.nullish(),
      startedAt: z.string().optional(),
      updatedAt: z.string().optional(),
      finishedAt: z.string().nullish(),
    })
    .nullish(),
  automationId: z.string().nullish(),
  automationRunId: z.string().nullish(),
  runtimeModelSources: runtimeModelSourcesSchema.nullish(),
  runtimeModelSourcesComplete: z.boolean().nullish(),
  extensionSnapshots: z.array(extensionSnapshotSchema).nullish(),
  extensionStates: z.array(provisioningExtensionSchema).nullish(),
})

const sandboxInputSpecSchema = z.preprocess(
  (value) => {
    if (!value || typeof value !== "object" || Array.isArray(value))
      return value
    const desired = { ...value } as Record<string, unknown>
    for (const key of Object.keys(sandboxObservedSpecSchema.shape))
      delete desired[key]
    return desired
  },
  executionInputSpecSchema.extend({
    runtimeId: z.string().trim().min(1, "请选择沙箱模板"),
    policy: z.string().optional(),
  })
)

export const resourceInputSchema = z
  .discriminatedUnion("kind", [
    resourceBaseSchema.extend({
      kind: z.literal("project"),
      spec: projectSpecSchema.strict(),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("image"),
      spec: imageSpecSchema
        .extend({
          reference: z
            .string()
            .trim()
            .min(1, "请填写镜像引用")
            .regex(/^\S+$/, "镜像引用不能包含空白字符"),
          architecture: z.enum(["all", "amd64", "arm64"]),
          modes: z
            .array(z.enum(["docker", "vm"]))
            .min(1, "请至少选择一种兼容类型"),
        })
        .strict(),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("runtime"),
      spec: executionInputSpecSchema.extend({
        driver: z.enum(["docker", "boxlite", "microsandbox", "vm"]),
        imageReference: z.string().trim().min(1, "请选择服务器上的镜像"),
      }),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("skill"),
      spec: skillSpecSchema.strict(),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("extension"),
      spec: extensionSpecSchema.strict(),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("mcp"),
      spec: mcpSpecSchema
        .extend({
          transport: z.enum(["stdio", "http"]),
        })
        .strict()
        .superRefine((spec, context) => {
          const key = spec.transport === "stdio" ? "command" : "url"
          if (!spec[key]?.trim())
            context.addIssue({
              code: "custom",
              path: [key],
              message:
                key === "command"
                  ? "stdio MCP 需要启动命令"
                  : "HTTP MCP 需要 URL",
            })
        }),
    }),
    resourceBaseSchema.extend({
      kind: z.literal("sandbox"),
      spec: sandboxInputSpecSchema,
    }),
    resourceBaseSchema.extend({
      kind: z.literal("variable"),
      spec: variableSpecSchema
        .extend({
          key: z.string().trim().min(1, "请填写环境变量名"),
          reference: z.string().trim().min(1, "请填写变量或密钥引用"),
        })
        .strict(),
    }),
  ])
  .superRefine((resource, context) => {
    if (resource.kind === "extension" && resource.enabled) {
      for (const [key, label] of [
        ["version", "固定版本"],
        ["installScript", "安装脚本"],
        ["verifyScript", "验证脚本"],
      ] as const) {
        if (!resource.spec[key].trim()) {
          context.addIssue({
            code: "custom",
            path: ["spec", key],
            message: `启用扩展前请填写${label}`,
          })
        }
      }
    }
    if (
      resource.kind !== "project" &&
      resource.kind !== "image" &&
      !resource.projectId
    ) {
      context.addIssue({
        code: "custom",
        path: ["projectId"],
        message: "请选择所属项目",
      })
    }
  })

const resourceResponseBaseSchema = resourceBaseSchema.extend({
  generation: z.number().int().positive().default(1),
  observedGeneration: z.number().int().nonnegative().default(0),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

// Old records can omit desired fields, but known values still have concrete types.
export const resourceSchema = z.discriminatedUnion("kind", [
  resourceResponseBaseSchema.extend({
    kind: z.literal("project"),
    spec: projectSpecSchema,
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("image"),
    spec: imageSpecSchema.partial(),
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("runtime"),
    spec: executionSpecSchema,
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("skill"),
    spec: skillSpecSchema,
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("extension"),
    spec: extensionSpecSchema,
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("mcp"),
    spec: mcpSpecSchema.partial(),
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("sandbox"),
    spec: executionSpecSchema.extend({
      runtimeId: z.string().optional(),
      policy: z.string().optional(),
      ...sandboxObservedSpecSchema.shape,
    }),
  }),
  resourceResponseBaseSchema.extend({
    kind: z.literal("variable"),
    spec: variableSpecSchema.partial(),
  }),
])

export const resourcesResponseSchema = z.object({
  resources: z.array(resourceSchema),
})

export const resourceResponseSchema = z.object({ resource: resourceSchema })

export type ResourceKind = z.infer<typeof resourceKindSchema>
export type ResourceInput = z.infer<typeof resourceInputSchema>
export type Resource = z.infer<typeof resourceSchema>
export type RuntimeModelSource = z.infer<typeof runtimeModelSourceSchema>
export type ResourceOfKind<K extends ResourceKind> = Extract<
  Resource,
  { kind: K }
>
// Editors intentionally hold incomplete fields until submit-time validation.
export type ResourceDraft = z.input<typeof resourceBaseSchema> & {
  kind: ResourceKind
  spec: Record<string, unknown>
}

// Template defaults are display fallbacks, not edits to an existing instance.
export function sandboxSpecForUpdate(
  draft: Record<string, unknown>,
  current: ResourceOfKind<"sandbox">["spec"]
) {
  const spec = { ...draft }
  for (const key of [
    "runtimeId",
    "serverId",
    "driver",
    "imageReference",
    "imageId",
    "cpu",
    "memory",
    "desktop",
    "network",
    "workdir",
    "setup",
    "extensionIds",
    "workspace",
    "proxyId",
  ] as const) {
    if (Object.hasOwn(current, key)) spec[key] = current[key]
    else delete spec[key]
  }
  return spec
}
export type ProvisioningProgress = z.infer<typeof provisioningProgressSchema>
