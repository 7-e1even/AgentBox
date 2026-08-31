import { ApiError } from "./api-client"

export type PollingState<T> = {
  data: T | undefined
  error: unknown
  loading: boolean
  stale: boolean
  updatedAt: number | null
}

export class PollingController<T> {
  private state: PollingState<T>
  private listeners = new Set<() => void>()
  private loader: ((signal: AbortSignal) => Promise<T>) | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private active: {
    controller: AbortController
    promise: Promise<T | undefined>
  } | null = null
  private epoch = 0
  private enabled = false
  private paused = false
  private interval: number | false | ((data: T | undefined) => number | false) =
    false
  private failures = 0

  constructor(initialData?: T) {
    this.state = {
      data: initialData,
      error: null,
      loading: false,
      stale: false,
      updatedAt: initialData === undefined ? null : Date.now(),
    }
  }

  getSnapshot = () => this.state
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  configure(
    loader: (signal: AbortSignal) => Promise<T>,
    interval: number | false | ((data: T | undefined) => number | false)
  ) {
    this.loader = loader
    const changed = this.interval !== interval
    this.interval = interval
    if (changed && this.enabled && !this.active) this.schedule()
  }

  start() {
    this.enabled = true
    if (!this.paused) void this.refresh()
  }

  stop() {
    this.enabled = false
    this.cancel()
  }

  pause(paused: boolean) {
    if (this.paused === paused) return
    this.paused = paused
    if (paused) this.cancel()
    else if (this.enabled) void this.refresh()
  }

  setData = (value: T | ((current: T | undefined) => T)) => {
    this.cancel()
    const data =
      typeof value === "function"
        ? (value as (current: T | undefined) => T)(this.state.data)
        : value
    this.failures = 0
    this.publish({
      data,
      error: null,
      loading: false,
      stale: false,
      updatedAt: Date.now(),
    })
    this.schedule()
  }

  refresh = (): Promise<T | undefined> => {
    if (this.active) return this.active.promise
    if (!this.loader || this.paused) return Promise.resolve(this.state.data)
    this.clearTimer()
    const controller = new AbortController()
    const epoch = ++this.epoch
    this.publish({ ...this.state, loading: true })
    const loader = this.loader
    const promise = Promise.resolve()
      .then(() => loader(controller.signal))
      .then((data) => {
        if (epoch !== this.epoch || controller.signal.aborted) return undefined
        this.failures = 0
        this.publish({
          data,
          error: null,
          loading: false,
          stale: false,
          updatedAt: Date.now(),
        })
        return data
      })
      .catch((error: unknown) => {
        if (epoch !== this.epoch || controller.signal.aborted) return undefined
        this.failures += 1
        this.publish({
          ...this.state,
          error,
          loading: false,
          stale: this.state.data !== undefined,
        })
        return undefined
      })
      .finally(() => {
        if (epoch !== this.epoch) return
        this.active = null
        this.schedule()
      })
    this.active = { controller, promise }
    return promise
  }

  private publish(state: PollingState<T>) {
    this.state = state
    this.listeners.forEach((listener) => listener())
  }

  private cancel() {
    this.clearTimer()
    this.epoch += 1
    this.active?.controller.abort()
    this.active = null
    if (this.state.loading) this.publish({ ...this.state, loading: false })
  }

  private clearTimer() {
    if (this.timer !== null) clearTimeout(this.timer)
    this.timer = null
  }

  private schedule() {
    this.clearTimer()
    if (!this.enabled || this.paused || this.active) return
    const error = this.state.error
    if (error instanceof ApiError && !error.retryable) return
    if (
      error !== null &&
      !(error instanceof ApiError) &&
      !(error instanceof TypeError)
    )
      return
    const interval =
      typeof this.interval === "function"
        ? this.interval(this.state.data)
        : this.interval
    if (interval === false && error === null) return
    let delay = interval || 5000
    if (this.failures > 0)
      delay = Math.min(60000, delay * 2 ** Math.min(this.failures - 1, 5))
    if (error instanceof ApiError && error.retryAfter) {
      const seconds = Number(error.retryAfter)
      const retryDelay = Number.isFinite(seconds)
        ? seconds * 1000
        : Date.parse(error.retryAfter) - Date.now()
      if (Number.isFinite(retryDelay)) delay = Math.max(delay, retryDelay)
    }
    this.timer = setTimeout(() => void this.refresh(), delay)
  }
}
