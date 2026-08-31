import { describe, expect, it } from "vitest"

import { resourceSchema, type Resource } from "./platform-schema"
import { resolveProjectId, resourcesForProject } from "./project-scope"

const timestamp = "2026-08-13T12:00:00.000Z"

function resource(
  id: string,
  kind: Resource["kind"],
  projectId: string | null
): Resource {
  return resourceSchema.parse({
    id,
    kind,
    projectId,
    name: id,
    description: "",
    enabled: true,
    spec: {},
    createdAt: timestamp,
    updatedAt: timestamp,
  })
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

  it("preserves the selection during an outage and resolves deleted projects after recovery", () => {
    expect(resolveProjectId(undefined, "deleted-project")).toBe(
      "deleted-project"
    )
    expect(resolveProjectId(resources, "deleted-project")).toBe("alpha")
    expect(resolveProjectId(undefined, "beta")).toBe("beta")
    expect(resolveProjectId(resources, "beta")).toBe("beta")
    expect(resolveProjectId([], "beta")).toBe("default")
  })
})
