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
