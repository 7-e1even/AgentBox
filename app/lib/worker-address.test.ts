import { describe, expect, it } from "vitest"

import { buildWorkerAddressCandidates, normalizeHttpOrigin } from "./worker-address"

describe("buildWorkerAddressCandidates", () => {
  it("prefers the configured public Worker address", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://127.0.0.1:3000",
        configuredOrigin: "https://agentbox.example/",
        interfaceAddresses: ["192.168.31.83"],
      })
    ).toEqual([
      { url: "https://agentbox.example", source: "configured" },
    ])
  })

  it("does not infer insecure LAN Worker addresses from a loopback gateway", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://127.0.0.1:3000",
        interfaceAddresses: ["192.168.31.83", "10.0.0.5"],
      })
    ).toEqual([])
  })

  it("rejects an incoming public HTTP gateway", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "http://192.168.31.83:3000",
        interfaceAddresses: ["172.18.0.3"],
      })
    ).toEqual([])
  })

  it("uses an HTTPS gateway and HTTPS inferred addresses", () => {
    expect(
      buildWorkerAddressCandidates({
        gatewayOrigin: "https://agentbox.example:3443",
        interfaceAddresses: ["192.168.31.83"],
      })
    ).toEqual([
      { url: "https://agentbox.example:3443", source: "gateway" },
      { url: "https://192.168.31.83:3443", source: "network-interface" },
    ])
  })
})

describe("normalizeHttpOrigin", () => {
  it("allows exact loopback HTTP and removes paths", () => {
    expect(normalizeHttpOrigin("http://127.0.0.1:3000/api/"))
      .toBe("http://127.0.0.1:3000")
    expect(normalizeHttpOrigin("http://localhost:3000/api/"))
      .toBe("http://localhost:3000")
    expect(normalizeHttpOrigin("http://[::1]:3000/api/"))
      .toBe("http://[::1]:3000")
  })

  it("requires HTTPS for non-loopback Worker addresses", () => {
    expect(normalizeHttpOrigin("http://192.168.31.83:3000")).toBeNull()
    expect(normalizeHttpOrigin("http://localhost.example:3000")).toBeNull()
    expect(normalizeHttpOrigin("http://127.example:3000")).toBeNull()
    expect(normalizeHttpOrigin("http://127.0.0.1.example:3000")).toBeNull()
    expect(normalizeHttpOrigin("https://agentbox.example/api/"))
      .toBe("https://agentbox.example")
  })

  it("rejects non-http URLs", () => {
    expect(normalizeHttpOrigin("ssh://192.168.31.83:22")).toBeNull()
  })
})
