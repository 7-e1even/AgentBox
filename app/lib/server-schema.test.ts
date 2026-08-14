import { describe, expect, it } from "vitest"

import { managedServerSchema } from "./server-schema"

describe("managedServerSchema", () => {
  it("parses Worker image inventory", () => {
    const server = managedServerSchema.parse({
      id: "62e2098f-5bef-406a-9cf6-846376f9fb46",
      name: "worker-one",
      hostname: "worker-one",
      os: "linux",
      arch: "amd64",
      capabilities: ["docker", "kvm-device"],
      inventory: {
        dockerImages: [
          {
            id: "sha256:abc",
            reference: "ubuntu:24.04",
            architecture: "amd64",
            size: "78.2MB",
            created: "12 days ago",
            format: "oci",
            path: "",
          },
        ],
        vmImages: [],
        vmImageDirectory: "/var/lib/agentbox/vm-images",
      },
      workerVersion: "v0.1.0",
      workerUpdateStatus: "",
      workerUpdateTarget: "",
      workerUpdateMessage: "",
      status: "online",
      lastSeenAt: "2026-08-13T10:39:53.616Z",
      createdAt: "2026-08-13T08:45:26.315Z",
      updatedAt: "2026-08-13T10:39:53.616Z",
    })

    expect(server.inventory.dockerImages[0]?.reference).toBe("ubuntu:24.04")
    expect(server.capabilities).toContain("kvm-device")
  })
})
