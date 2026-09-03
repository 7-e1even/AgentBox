import { describe, expect, it } from "vitest"

import { createLogsQuery } from "./logs"

const filter = {
  page: 1,
  pageSize: 50,
  category: "all",
  level: "all",
  status: "all",
  query: "",
  timeRange: "24h" as const,
  before: null,
}

describe("log query time boundaries", () => {
  it("sends absolute timestamps across a local midnight instead of UTC dates", () => {
    const { params } = createLogsQuery(
      filter,
      new Date("2026-08-31T00:30:00+08:00")
    )

    expect(params.get("from")).toBe("2026-08-29T16:30:00.000Z")
    expect(params.get("to")).toBe("2026-08-30T16:30:00.000Z")
  })

  it("keeps the first page's time window while browsing later pages", () => {
    const first = createLogsQuery(filter, new Date("2026-08-31T02:00:00Z"))
    const next = createLogsQuery(
      { ...filter, page: 2, before: first.to },
      new Date("2026-09-01T02:00:00Z")
    )

    expect(next.from).toBe(first.from)
    expect(next.to).toBe(first.to)
    expect(next.params.get("page")).toBe("2")
  })

  it("includes older history without moving the pagination boundary", () => {
    const { params } = createLogsQuery({
      ...filter,
      timeRange: "all",
      page: 3,
      before: "2026-08-31T02:00:00Z",
    })

    expect(params.has("from")).toBe(false)
    expect(params.get("to")).toBe("2026-08-31T02:00:00.000Z")
  })
})
