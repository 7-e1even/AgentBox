import { z } from "zod"

export const sandboxFileEntrySchema = z.object({
  name: z.string(),
  path: z.string(),
  type: z.enum(["directory", "file"]),
  size: z.number().nonnegative(),
  modifiedAt: z.number().nonnegative(),
})

export const sandboxFileEntriesSchema = z.array(sandboxFileEntrySchema)

export type SandboxFileEntry = z.infer<typeof sandboxFileEntrySchema>
