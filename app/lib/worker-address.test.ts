import { describe, expect, it } from "vitest"

import { buildWorkerAddressCandidates, normalizeHttpOrigin } from "./worker-address"

describe("buildWorkerAddressCandidates", () => {
  it("prefers the configured public Worker address", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://127.0.0.1:3000",
        configuredOrigin: "http://192.168.31.83:3000/",
        interfaceAddresses: ["192.168.31.83"],
      })
    ).toEqual([
      { url: "http://192.168.31.83:3000", source: "configured" },
    ])
  })

  it("replaces the loopback gateway host with reachable network interfaces", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://127.0.0.1:3000",
        interfaceAddresses: ["192.168.31.83", "10.0.0.5"],
      })
    ).toEqual([
      { url: "http://192.168.31.83:3000", source: "network-interface" },
      { url: "http://10.0.0.5:3000", source: "network-interface" },
    ])
  })

  it("uses an explicitly configured public gateway before inferred addresses", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://127.0.0.1:3000",
        configuredOrigin: "https://agentbox.example",
        interfaceAddresses: ["192.168.31.83"],
      })
    ).toEqual([
      { url: "https://agentbox.example", source: "configured" },
      { url: "http://192.168.31.83:3000", source: "network-interface" },
    ])
  })
})

describe("normalizeHttpOrigin", () => {
  it("removes paths and trailing slashes", () => {
    expect(normalizeHttpOrigin("http://192.168.31.83:3000/api/"))
      .toBe("http://192.168.31.83:3000")
  })

  it("rejects non-http URLs", () => {
    expect(normalizeHttpOrigin("ssh://192.168.31.83:22")).toBeNull()
  })
})
