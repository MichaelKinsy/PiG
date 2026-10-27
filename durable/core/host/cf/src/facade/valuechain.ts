// The values of the documents a published notice carried, rebuilt lazily (core/tx.go sentValues): the core leaves out the value of a
// document change whose previous value this host holds, so a notice costs the operations, not the whole document. A value is made when
// something reads it, from the nearest value the host has and the operations since, folded (Chord's PendingOps) so that a run of partial
// commits that nobody read costs one application, and one that was read costs what Pi's watchers pay.
import { applyImmutable, PendingOps, type Op } from "@earendil-works/chord/delta"

/** One revision of a document: its value once it is known, else the operations that make it of `prev`. */
export class Revision {
  value: unknown
  known: boolean
  prev: Revision | undefined
  readonly ops: readonly Op[]
  /** Operations of the revisions since the last known one, which a value read must apply. */
  distance: number

  private constructor(known: boolean, value: unknown, prev: Revision | undefined, ops: readonly Op[]) {
    this.known = known; this.value = value; this.prev = prev; this.ops = ops
    this.distance = prev === undefined ? 0 : prev.known ? ops.length : prev.distance + ops.length
  }

  /** A revision whose value arrived whole. */
  static of(value: unknown): Revision { return new Revision(true, value, undefined, []) }

  /** The revision `ops` make of this one; its value is made when read, or now when the operations waiting have grown past what a fold keeps. */
  next(ops: readonly Op[]): Revision {
    const revision = new Revision(false, undefined, this, ops)
    if (revision.distance > MAX_UNREAD_OPS) revision.read()
    return revision
  }

  /** The value of this revision. */
  read(): unknown {
    if (this.known) return this.value
    const chain: Revision[] = []
    let at: Revision = this
    while (!at.known) { chain.push(at); at = at.prev! }
    const pending = new PendingOps()
    for (let i = chain.length - 1; i >= 0; i--) pending.push(chain[i]!.ops)
    const taken = pending.take()
    let value = at.value
    if (taken.kind === "ops") value = applyImmutable(value as never, taken.ops as never)
    // Past Chord's cap on folded operations only "replace" stays pending: apply the revisions one by one.
    else for (let i = chain.length - 1; i >= 0; i--) value = applyImmutable(value as never, chain[i]!.ops as never)
    this.value = value
    this.known = true
    this.prev = undefined
    this.distance = 0
    return value
  }
}

/** Operations a run of unread revisions may hold before the value is made, which keeps a fold inside PendingOps' cap. */
const MAX_UNREAD_OPS = 4_000

/** Make `change.value` read its revision when something first reads it. */
export function lazyValue(change: { value?: unknown }, revision: Revision): void {
  Object.defineProperty(change, "value", {
    configurable: true,
    enumerable: true,
    get: () => {
      const value = revision.read()
      Object.defineProperty(change, "value", { value, writable: true, configurable: true, enumerable: true })
      return value
    },
    set: (value: unknown) => { Object.defineProperty(change, "value", { value, writable: true, configurable: true, enumerable: true }) },
  })
}
