import { describe, expect, it } from "vitest"

import type { ManagedServer } from "./server-schema"
import {
  normalizeRuntimeImageReference,
  runtimeImageChoices,
  runtimeInventoryImages,
  usesRuntimeImageInventory,
} from "./runtime-images"

const server = {
  inventory: {
    dockerImages: [
      {
        id: "sha256:docker",
        reference: "ubuntu:24.04",
        architecture: "amd64",
        size: "78MB",
        created: "today",
        format: "oci",
        path: "",
      },
    ],
    boxliteImages: [
      {
        id: "boxlite",
        reference: "alpine:3.20",
        architecture: "amd64",
        size: "8 MiB",
        created: "today",
        format: "oci",
        path: "",
      },
    ],
    microsandboxImages: [
      {
        id: "sha256:micro",
        reference: "python:3.12",
        architecture: "amd64",
        size: "125 MiB",
        created: "today",
        format: "oci",
        path: "",
      },
    ],
    vmImages: [
      {
        id: "vm-one",
        reference: "ubuntu-24.04.qcow2",
        architecture: "amd64",
        size: "2GB",
        created: "today",
        format: "qcow2",
        path: "/var/lib/agentbox/vm-images/ubuntu-24.04.qcow2",
      },
    ],
    vmImageDirectory: "/var/lib/agentbox/vm-images",
  },
} as ManagedServer

describe("runtime images", () => {
  it("shares the unified OCI inventory across container runtimes", () => {
    const expected = ["ubuntu:24.04", "alpine:3.20", "python:3.12"]

    expect(
      runtimeInventoryImages(server, "docker").map((image) => image.reference)
    ).toEqual(expected)
    expect(
      runtimeInventoryImages(server, "boxlite").map((image) => image.reference)
    ).toEqual(expected)
    expect(
      runtimeInventoryImages(server, "microsandbox").map(
        (image) => image.reference
      )
    ).toEqual(expected)
  })

  it("keeps uncached Docker registry references separate", () => {
    const choices = runtimeImageChoices(
      server,
      "docker",
      "registry.example.com/agent:latest"
    )

    expect(choices.local.map((option) => option.value)).toEqual([
      "ubuntu:24.04",
      "alpine:3.20",
      "python:3.12",
    ])
    expect(choices.registry.map((option) => option.value)).toEqual([
      "registry.example.com/agent:latest",
    ])
  })

  it("uses the same local choices for Microsandbox", () => {
    const choices = runtimeImageChoices(
      server,
      "microsandbox",
      "registry.example.com/agent:latest"
    )

    expect(choices.local.map((option) => option.value)).toEqual([
      "ubuntu:24.04",
      "alpine:3.20",
      "python:3.12",
    ])
    expect(choices.registry).toEqual([
      {
        value: "registry.example.com/agent:latest",
        label:
          "registry.example.com/agent:latest · 创建时导入或拉取",
      },
    ])
  })

  it("exposes searchable inventory for all runtimes", () => {
    expect(usesRuntimeImageInventory("docker")).toBe(true)
    expect(usesRuntimeImageInventory("boxlite")).toBe(true)
    expect(usesRuntimeImageInventory("microsandbox")).toBe(true)
    expect(normalizeRuntimeImageReference(server, "boxlite", "")).toBe(
      "ubuntu:24.04"
    )
  })

  it("only selects Worker-local VM images for the VM driver", () => {
    expect(usesRuntimeImageInventory("vm")).toBe(true)
    expect(normalizeRuntimeImageReference(server, "vm", "ubuntu:24.04")).toBe(
      "ubuntu-24.04.qcow2"
    )
  })
})
