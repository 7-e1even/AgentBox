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
  it("only exposes Docker inventory to the Docker driver", () => {
    expect(runtimeInventoryImages(server, "docker")).toHaveLength(1)
    expect(runtimeInventoryImages(server, "boxlite")).toEqual([])
    expect(runtimeInventoryImages(server, "microsandbox")).toEqual([])
  })

  it("keeps uncached Docker registry references separate", () => {
    const choices = runtimeImageChoices(
      server,
      "docker",
      "registry.example.com/agent:latest"
    )

    expect(choices.local.map((option) => option.value)).toEqual([
      "ubuntu:24.04",
    ])
    expect(choices.registry.map((option) => option.value)).toEqual([
      "registry.example.com/agent:latest",
    ])
  })

  it("uses registry input instead of Docker inventory for MicroVM drivers", () => {
    expect(usesRuntimeImageInventory("boxlite")).toBe(false)
    expect(usesRuntimeImageInventory("microsandbox")).toBe(false)
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
