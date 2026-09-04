import { describe, expect, it } from "vitest"

import {
  readSkillFrontmatter,
  skillDocumentIssue,
  syncSkillDocument,
} from "./skill-frontmatter"

describe("Skill frontmatter", () => {
  it("reads canonical metadata and preserves optional provenance fields", () => {
    const result = readSkillFrontmatter(
      [
        "---",
        "name: repo-review",
        'description: "Review a repository"',
        "license: MIT",
        "compatibility: Requires git 2.40 or later",
        "---",
        "Inspect the repository.",
      ].join("\n")
    )
    expect(result).toEqual({
      metadata: {
        name: "repo-review",
        description: "Review a repository",
        license: "MIT",
        compatibility: "Requires git 2.40 or later",
      },
      issues: [],
    })
  })

  it("requires identity, description, and an instruction body", () => {
    expect(
      skillDocumentIssue(
        "---\nname: other\ndescription: Review files\n---\nDo the work.\n",
        "repo-review"
      )
    ).toContain("唯一标识")
    expect(
      skillDocumentIssue(
        "---\nname: repo-review\ndescription: ''\n---\nDo the work.\n",
        "repo-review"
      )
    ).toContain("description")
    expect(
      skillDocumentIssue(
        "---\nname: repo-review\ndescription: Review files\n---\n",
        "repo-review"
      )
    ).toContain("指令正文")
  })

  it("requires the same exact frontmatter delimiters as the API", () => {
    const body = "name: repo-review\ndescription: Review files"
    expect(
      skillDocumentIssue(`--- \n${body}\n---\nDo the work.\n`, "repo-review")
    ).toContain("开头")
    expect(
      skillDocumentIssue(`---\n${body}\n--- \nDo the work.\n`, "repo-review")
    ).toContain("结束标记")
    expect(
      skillDocumentIssue(` ---\n${body}\n---\nDo the work.\n`, "repo-review")
    ).toContain("开头")
  })

  it("repairs legacy documents while preserving extra metadata and body", () => {
    const result = syncSkillDocument(
      [
        "---",
        "name: old-name",
        "description: old description",
        "license: Apache-2.0",
        "---",
        "Keep this body.",
      ].join("\n"),
      { name: "new-name", description: "New description" }
    )
    expect(result).toContain('name: "new-name"')
    expect(result).toContain('description: "New description"')
    expect(result).toContain("license: Apache-2.0")
    expect(result).toContain("Keep this body.")
  })

  it("wraps a frontmatter-less legacy body in a canonical document", () => {
    expect(
      syncSkillDocument("Follow the instructions.", {
        name: "repo-review",
        description: "Review files",
      })
    ).toBe(
      '---\nname: "repo-review"\ndescription: "Review files"\n---\nFollow the instructions.\n'
    )
  })
})
