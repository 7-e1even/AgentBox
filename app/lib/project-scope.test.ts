import { describe, expect, it } from "vitest"

import type { Resource } from "./platform-schema"
import { resolveProjectId, resourcesForProject } from "./project-scope"

const timestamp = "2026-08-13T12:00:00.000Z"

function resource(
  id: string,
  kind: Resource["kind"],
  projectId: string | null
): Resource {
  return {
    id,
    kind,
    projectId,
    name: id,
    description: "",
    enabled: true,
    spec: {},
    createdAt: timestamp,
    updatedAt: timestamp,
  }
}

describe("project scope", () => {
  const resources = [
    resource("alpha", "project", null),
    resource("beta", "project", null),
    resource("shared-image", "image", null),
    resource("alpha-runtime", "runtime", "alpha"),
    resource("beta-runtime", "runtime", "beta"),
  ]

  it("keeps a valid preferred project and falls back safely", () => {
    expect(resolveProjectId(resources, "beta")).toBe("beta")
    expect(resolveProjectId(resources, "missing")).toBe("alpha")
    expect(resolveProjectId([], "missing")).toBe("default")
  })

  it("returns only the selected project plus global images", () => {
    expect(
      resourcesForProject(resources, "beta").map((item) => item.id)
    ).toEqual(["beta", "shared-image", "beta-runtime"])
  })
})
