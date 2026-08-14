import { z } from "zod"

export const serverImageSchema = z.object({
  id: z.string(),
  reference: z.string(),
  architecture: z.string(),
  size: z.string(),
  created: z.string(),
  format: z.string(),
  path: z.string(),
})

export const serverInventorySchema = z.object({
  dockerImages: z.array(serverImageSchema),
  vmImages: z.array(serverImageSchema),
  vmImageDirectory: z.string(),
})

export const managedServerSchema = z.object({
  id: z.string().uuid(),
  name: z.string(),
  hostname: z.string(),
  os: z.string(),
  arch: z.string(),
  capabilities: z.array(z.string()),
  inventory: serverInventorySchema,
  workerVersion: z.string(),
  workerUpdateStatus: z.enum(["", "pending", "updating", "succeeded", "failed"]),
  workerUpdateTarget: z.string(),
  workerUpdateMessage: z.string(),
  status: z.enum(["online", "offline"]),
  lastSeenAt: z.string().datetime({ offset: true }),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
})

export const serversResponseSchema = z.object({
  servers: z.array(managedServerSchema),
  workerVersion: z.string(),
})

export const serverPairingSchema = z.object({
  id: z.string().uuid(),
  token: z.string().optional(),
  expiresAt: z.string().datetime({ offset: true }),
  serverId: z.string().uuid().nullable(),
  claimedAt: z.string().datetime({ offset: true }).nullable(),
})

export const serverPairingResponseSchema = z.object({
  pairing: serverPairingSchema,
})

export type ManagedServer = z.infer<typeof managedServerSchema>
export type ServerImage = z.infer<typeof serverImageSchema>
export type ServerPairing = z.infer<typeof serverPairingSchema>
