import { afterEach, describe, expect, it, vi } from "vitest"
import { ApiError } from "./api-client"
import { PollingController } from "./polling-controller"

afterEach(() => vi.useRealTimers())

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe("PollingController", () => {
  it("joins manual refresh and schedules only after the active request finishes", async () => {
    vi.useFakeTimers()
    const pending = deferred<number>()
    const load = vi.fn(() => pending.promise)
    const polling = new PollingController<number>()
    polling.configure(load, 1000)
    polling.start()
    const first = polling.refresh()
    expect(polling.refresh()).toBe(first)
    await vi.advanceTimersByTimeAsync(5000)
    expect(load).toHaveBeenCalledTimes(1)
    pending.resolve(1)
    await first
    await vi.advanceTimersByTimeAsync(999)
    expect(load).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(load).toHaveBeenCalledTimes(2)
    polling.stop()
  })

  it("aborts on stop and fences loaders that ignore abort", async () => {
    const pending = deferred<number>()
    const polling = new PollingController(2)
    let signal!: AbortSignal
    polling.configure((next) => {
      signal = next
      return pending.promise
    }, false)
    const request = polling.refresh()
    await Promise.resolve()
    polling.stop()
    expect(signal.aborted).toBe(true)
    pending.resolve(1)
    await request
    expect(polling.getSnapshot().data).toBe(2)
  })

  it("keeps stale data, backs off, honors Retry-After, and resets after success", async () => {
    vi.useFakeTimers()
    const load = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("offline"))
      .mockRejectedValueOnce(
        new ApiError("busy", { status: 429, retryable: true, retryAfter: "4" })
      )
      .mockResolvedValue(3)
    const polling = new PollingController(2)
    polling.configure(load, 1000)
    polling.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(polling.getSnapshot()).toMatchObject({
      data: 2,
      stale: true,
      loading: false,
    })
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(3999)
    expect(load).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(polling.getSnapshot()).toMatchObject({
      data: 3,
      stale: false,
      error: null,
    })
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(4)
    polling.stop()
  })

  it.each([
    new ApiError("unauthorized", { status: 401 }),
    new ApiError("forbidden", { status: 403 }),
    new ApiError("not found", { status: 404 }),
    new Error("invalid response"),
  ])("does not retry permanent failures automatically: %s", async (error) => {
    vi.useFakeTimers()
    const load = vi.fn().mockRejectedValue(error)
    const polling = new PollingController<number>()
    polling.configure(load, 100)
    polling.start()
    await vi.advanceTimersByTimeAsync(10000)
    expect(load).toHaveBeenCalledTimes(1)
    await polling.refresh()
    expect(load).toHaveBeenCalledTimes(2)
    polling.stop()
  })

  it("pauses background work and refreshes immediately when visible again", async () => {
    vi.useFakeTimers()
    const load = vi.fn().mockResolvedValue(1)
    const polling = new PollingController<number>()
    polling.configure(load, 100)
    polling.pause(true)
    polling.start()
    await vi.advanceTimersByTimeAsync(500)
    expect(load).not.toHaveBeenCalled()
    polling.pause(false)
    await vi.advanceTimersByTimeAsync(0)
    expect(load).toHaveBeenCalledTimes(1)
    polling.pause(true)
    await vi.advanceTimersByTimeAsync(500)
    expect(load).toHaveBeenCalledTimes(1)
    polling.stop()
  })

  it("local mutation cancels a stale read before it can overwrite newer data", async () => {
    const pending = deferred<number>()
    const polling = new PollingController(1)
    polling.configure(() => pending.promise, false)
    const request = polling.refresh()
    polling.setData(3)
    pending.resolve(2)
    await request
    expect(polling.getSnapshot().data).toBe(3)
  })

  it("stops conditional polling as soon as work reaches a terminal state", async () => {
    vi.useFakeTimers()
    const load = vi
      .fn()
      .mockResolvedValueOnce("running")
      .mockResolvedValue("complete")
    const polling = new PollingController<string>()
    polling.configure(load, (state) => (state === "running" ? 100 : false))
    polling.start()
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(2)
    expect(polling.getSnapshot().data).toBe("complete")
    polling.stop()
  })

  it("retries a transient initial domain failure without turning it into an empty success", async () => {
    vi.useFakeTimers()
    const load = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("offline"))
      .mockResolvedValue([])
    const polling = new PollingController<string[]>()
    polling.configure(load, false)
    polling.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(polling.getSnapshot()).toMatchObject({
      data: undefined,
      loading: false,
    })
    expect(polling.getSnapshot().error).toBeInstanceOf(TypeError)
    await vi.advanceTimersByTimeAsync(5000)
    expect(polling.getSnapshot()).toMatchObject({ data: [], error: null })
    await vi.advanceTimersByTimeAsync(10000)
    expect(load).toHaveBeenCalledTimes(2)
    polling.stop()
  })
})
