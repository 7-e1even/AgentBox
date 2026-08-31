import { describe, expect, it } from "vitest"

import { resourceInputSchema, resourceSchema } from "./platform-schema"
import {
  skillCatalogId,
  skillImportResponseSchema,
  skillSearchResponseSchema,
} from "./skill-import"

describe("imported Skill resource contract", () => {
  it("recognizes an imported catalog skill across supported skills.sh URL forms", () => {
    expect(
      skillCatalogId(
        "https://www.skills.sh/owner/repo/react%3Acomponents/?from=search"
      )
    ).toBe("owner/repo/react:components")
    expect(
      skillCatalogId("https://skills.sh/owner/repo/react:components")
    ).toBe("owner/repo/react:components")
    for (const source of [
      "upload.zip",
      "javascript:alert(1)",
      "https://skills.sh.example.com/owner/repo/skill",
      "https://skills.sh/",
    ]) {
      expect(skillCatalogId(source)).toBeNull()
    }
  })

  it("requires structured search results instead of treating upstream errors as an empty catalog", () => {
    expect(
      skillSearchResponseSchema.safeParse({
        query: "react",
        skills: [],
        excluded: 0,
      }).success
    ).toBe(true)
    expect(
      skillSearchResponseSchema.safeParse({ error: "unavailable" }).success
    ).toBe(false)
  })

  it("preserves complete markdown and binary attachments through preview, save and readback", () => {
    const spec = {
      source: "upload",
      path: "example.zip",
      instructions:
        "---\nname: example\ndescription: Review files\nlicense: MIT\n---\nRead references/guide.md.\n",
      files: [
        {
          path: "scripts/check.sh",
          content: "IyEvYmluL3NoCg==",
          executable: true,
        },
        { path: "assets/sample.bin", content: "AP/+AA==" },
      ],
    }
    const { skill } = skillImportResponseSchema.parse({
      skill: { name: "example", description: "Review files", spec },
    })
    const input = resourceInputSchema.parse({
      id: "example",
      kind: "skill",
      projectId: "default",
      enabled: true,
      ...skill,
    })
    const saved = resourceSchema.parse(
      JSON.parse(
        JSON.stringify({
          ...input,
          createdAt: "2026-08-31T00:00:00Z",
          updatedAt: "2026-08-31T00:00:00Z",
        })
      )
    )
    expect(saved.spec).toEqual(spec)
  })

  it("rejects a malformed attachment instead of silently dropping it", () => {
    expect(
      skillImportResponseSchema.safeParse({
        skill: {
          name: "example",
          description: "Review files",
          spec: {
            files: [{ path: "scripts/check.sh", content: 123 }],
          },
        },
      }).success
    ).toBe(false)
  })
})
