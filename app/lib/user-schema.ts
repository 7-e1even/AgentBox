import { z } from "zod"

export const userRoleSchema = z.enum(["admin", "operator", "viewer"])
export const userStatusSchema = z.enum(["active", "disabled"])

export const userPreferencesSchema = z.object({
  successNotifications: z.boolean(),
  density: z.enum(["comfortable", "compact"]),
  showCapabilities: z.boolean(),
  showInfrastructure: z.boolean(),
  showGovernance: z.boolean(),
})

export const defaultUserPreferences = {
  successNotifications: true,
  density: "comfortable",
  showCapabilities: true,
  showInfrastructure: true,
  showGovernance: true,
} as const

export const userSchema = z.object({
  id: z.string().uuid(),
  name: z.string().min(2).max(80),
  username: z.string().regex(/^[a-z0-9][a-z0-9._-]{2,63}$/),
  email: z.string().email(),
  role: userRoleSchema,
  status: userStatusSchema,
  preferences: userPreferencesSchema.default(defaultUserPreferences),
  lastLoginAt: z.string().datetime({ offset: true }).nullable(),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

export const userInputSchema = z.object({
  name: z.string().trim().min(2, "名称至少需要 2 个字符").max(80),
  username: z
    .string()
    .trim()
    .toLowerCase()
    .min(3, "用户名至少需要 3 个字符")
    .max(64, "用户名最多 64 个字符")
    .regex(
      /^[a-z0-9][a-z0-9._-]*$/,
      "用户名只能包含字母、数字、点、下划线和短横线"
    ),
  email: z.string().trim().email("请输入有效的邮箱地址").max(254),
  password: z
    .string()
    .max(128)
    .refine(
      (value) => value === "" || value.length >= 8,
      "密码至少需要 8 个字符"
    ),
  role: userRoleSchema,
  status: userStatusSchema,
})

export const userResponseSchema = z.object({ user: userSchema })
export const usersResponseSchema = z.object({ users: z.array(userSchema) })
export const authStatusSchema = z.object({ needsSetup: z.boolean() })

export type ManagedUser = z.infer<typeof userSchema>
export type UserInput = z.infer<typeof userInputSchema>
export type UserRole = z.infer<typeof userRoleSchema>
export type UserStatus = z.infer<typeof userStatusSchema>
export type UserPreferences = z.infer<typeof userPreferencesSchema>
