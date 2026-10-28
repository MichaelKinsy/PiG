// Timers and the object alarm of a Durable Object host (ADR D7, D8).
//
//   volatile timers (throttle windows)  setTimeout: they exist only while a stream or tool already holds the object
//   durable timers (sleep, retry, poll)  no setTimeout: a pending timer would block hibernation; the object alarm wakes it
//   liveness watchdog                     the same alarm: the earliest of the watchdog and every durable timer
//
// One alarm per object. Writing it costs a row, so it is written only when the target time changes. An early or duplicate
// alarm is harmless: `due` fires only what is due and the core re-checks due times.
import type { TimerDriver } from "./host.ts"

export interface AlarmStorage {
  setAlarm(scheduledTime: number): Promise<void> | void
  deleteAlarm(): Promise<void> | void
}

export class AlarmTimers implements TimerDriver {
  /** Alarm writes (setAlarm and deleteAlarm calls): the count the billing model needs (ADR D13). */
  alarmWrites = 0
  private readonly handles = new Map<number, ReturnType<typeof setTimeout>>()
  private readonly durable = new Map<number, number>()
  private watchdog = -1
  private armed = -1
  private readonly storage: AlarmStorage
  private readonly fire: (timerId: number) => void
  private readonly clock: () => number
  private readonly onError: (error: unknown) => void

  constructor(storage: AlarmStorage, fire: (timerId: number) => void, clock: () => number = Date.now, onError: (error: unknown) => void = () => {}) {
    this.storage = storage
    this.fire = fire
    this.clock = clock
    this.onError = onError
  }

  /** Volatile timers wait in this isolate; a durable one waits in the alarm. */
  get volatilePending(): number { return this.handles.size }
  get durablePending(): number { return this.durable.size }
  get alarmAt(): number { return this.armed }

  set(timerId: number, at: number, timerClass: number) {
    this.clear(timerId)
    // A background timer only frees memory, and a pending timer would block hibernation: declined, the core frees it at later commits.
    if (timerClass === 2) return
    if (timerClass === 1) { this.durable.set(timerId, at); this.arm(); return }
    this.handles.set(timerId, setTimeout(() => { this.handles.delete(timerId); this.fire(timerId) }, Math.max(0, at - this.clock())))
  }

  clear(timerId: number) {
    const handle = this.handles.get(timerId)
    if (handle !== undefined) { clearTimeout(handle); this.handles.delete(timerId) }
    if (this.durable.delete(timerId)) this.arm()
  }

  liveness(at: number) {
    this.watchdog = at
    this.arm()
  }

  /** The object alarm fired: the alarm is consumed, every due durable timer runs, and the watchdog is spent if it was due. */
  due(now: number = this.clock()) {
    this.armed = -1
    if (this.watchdog >= 0 && this.watchdog <= now) this.watchdog = -1
    for (const [id, at] of [...this.durable]) if (at <= now) { this.durable.delete(id); this.fire(id) }
    this.arm()
  }

  /** Forget everything without touching the alarm: the handle was discarded and the next open re-derives its timers. */
  stop() {
    for (const h of this.handles.values()) clearTimeout(h)
    this.handles.clear()
    this.durable.clear()
    this.watchdog = -1
  }

  /** The host recycled its handle: the reopened core arms its own timers from the store. The alarm stays where it is; an early one is harmless. */
  reset() { this.stop() }

  private arm() {
    const times = [...this.durable.values(), ...(this.watchdog >= 0 ? [this.watchdog] : [])]
    const at = times.length ? Math.min(...times) : -1
    if (at === this.armed) return
    const previous = this.armed
    this.armed = at
    this.alarmWrites++
    // A write that failed left the platform alarm where it was: forget the target, or the next change to the same time is skipped
    // as "already armed" and the watchdog never gets written.
    const failed = (error: unknown) => { if (this.armed === at) this.armed = previous; this.onError(error) }
    let done: Promise<void> | void
    try { done = at < 0 ? this.storage.deleteAlarm() : this.storage.setAlarm(at) } catch (error) { return failed(error) }
    if (done) void done.catch(failed)
  }
}

/**
 * Timers of a host that stays alive (a Node process): every timer, durable or not, is a setTimeout of the Harness clock's
 * distance, and the liveness watchdog needs no timer because the process is the liveness. A durable timer does not survive
 * the process; the core re-arms it on the next open (its task is pending again).
 */
export class TimeoutTimers implements TimerDriver {
  private readonly handles = new Map<number, ReturnType<typeof setTimeout>>()
  private readonly fire: (timerId: number) => void
  private readonly clock: () => number

  constructor(fire: (timerId: number) => void, clock: () => number = Date.now) { this.fire = fire; this.clock = clock }

  set(timerId: number, at: number, _timerClass?: number) {
    this.clear(timerId)
    this.handles.set(timerId, setTimeout(() => { this.handles.delete(timerId); this.fire(timerId) }, Math.max(0, at - this.clock())))
  }

  clear(timerId: number) {
    const handle = this.handles.get(timerId)
    if (handle !== undefined) { clearTimeout(handle); this.handles.delete(timerId) }
  }

  liveness() {}

  /** Cancel every pending timer: the handle was closed or discarded. */
  dispose() { for (const handle of this.handles.values()) clearTimeout(handle); this.handles.clear() }

  /** The host recycled its handle: the reopened core arms its own timers from the store. */
  reset() { this.dispose() }
}

/** HarnessOptions.setTimer (types.ts SetTimer): arms `callback` after `delayMs`; `undefined` declines a background timer. */
export type SetTimer = (callback: () => void, delayMs: number, options: { readonly background: boolean }) => (() => void) | undefined

/** Longest delay `setTimeout` supports; a longer one is waited for in several steps (packages/durable/src/timers.ts). */
const MAX_TIMER_DELAY = 2_147_483_647

/**
 * The default `HarnessOptions.setTimer`: `setTimeout`, waiting out delays beyond its limit in steps (packages/durable/src/timers.ts
 * nativeTimer). A background timer is unreferenced, so it never keeps the process alive; it is declined where timers cannot be.
 */
export const nativeTimer: SetTimer = (callback, delayMs, options) => {
  if (options.background && typeof navigator !== "undefined" && navigator.userAgent === "Cloudflare-Workers") return undefined
  const arm = (remaining: number): ReturnType<typeof setTimeout> => {
    const next = setTimeout(() => {
      if (remaining > MAX_TIMER_DELAY) handle = arm(remaining - MAX_TIMER_DELAY)
      else callback()
    }, Math.min(remaining, MAX_TIMER_DELAY))
    if (options.background) (next as { unref?: () => void }).unref?.()
    return next
  }
  let handle = arm(Math.max(0, delayMs))
  if (options.background && typeof (handle as { unref?: unknown }).unref !== "function") { clearTimeout(handle); return undefined }
  return () => clearTimeout(handle)
}

/** Timers the Session and Harness arm through the host's `setTimer` (packages/durable/src/timers.ts Timers). */
export interface HostTimers {
  /** A timer that paces or resumes work; when the host throws or declines it, `setTimeout` arms it instead. */
  foreground(callback: () => void, delayMs: number): () => void
  /** A timer that only frees memory; `undefined` when the host declines it. */
  background(callback: () => void, delayMs: number): (() => void) | undefined
}

/**
 * Wrap a host `setTimer` (packages/durable/src/timers.ts hostTimers). A foreground timer must fire, or work stalls: one the host
 * throws on or declines is armed with `setTimeout`, and the first such failure is reported. A background one the host throws on is
 * reported and declined.
 */
export function hostTimers(setTimer: SetTimer | undefined, report: (error: unknown) => void): HostTimers {
  const arm = setTimer ?? nativeTimer
  let reported = false
  const fallBack = (error: unknown) => { if (reported) return; reported = true; report(error) }
  return {
    foreground: (callback, delayMs) => {
      try {
        const disarm = arm(callback, delayMs, { background: false })
        if (disarm !== undefined) return disarm
        fallBack(new Error("HarnessOptions.setTimer declined a timer that paces work; using setTimeout"))
      } catch (error) { fallBack(error) }
      return nativeTimer(callback, delayMs, { background: false })!
    },
    background: (callback, delayMs) => {
      try { return arm(callback, delayMs, { background: true }) } catch (error) { report(error); return undefined }
    },
  }
}

/**
 * The core's timers through the host's `setTimer` (HarnessOptions.setTimer): a volatile or durable timer paces work, so it is a
 * foreground one; a background timer only frees memory, and the host may decline it, which the core copes with at later commits.
 */
export class SetTimerTimers implements TimerDriver {
  private readonly disarms = new Map<number, () => void>()
  private readonly timers: HostTimers
  private readonly fire: (timerId: number) => void
  private readonly clock: () => number

  constructor(timers: HostTimers, fire: (timerId: number) => void, clock: () => number = Date.now) {
    this.timers = timers; this.fire = fire; this.clock = clock
  }

  set(timerId: number, at: number, timerClass: number) {
    this.clear(timerId)
    const callback = () => { this.disarms.delete(timerId); this.fire(timerId) }
    const delay = Math.max(0, at - this.clock())
    const disarm = timerClass === 2 ? this.timers.background(callback, delay) : this.timers.foreground(callback, delay)
    if (disarm !== undefined) this.disarms.set(timerId, disarm)
  }

  clear(timerId: number) {
    const disarm = this.disarms.get(timerId)
    if (disarm !== undefined) { this.disarms.delete(timerId); disarm() }
  }

  liveness() {}

  /** Disarm every pending timer: the handle was closed or discarded. */
  dispose() { for (const disarm of this.disarms.values()) disarm(); this.disarms.clear() }

  /** The host recycled its handle: the reopened core arms its own timers from the store. */
  reset() { this.dispose() }
}
