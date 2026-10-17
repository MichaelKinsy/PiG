// Watches and attached states over the core's `watch.open` / `watch.close` api requests (ABI section 8.1). The core answers
// `watch.open` with the first frame and then publishes every later frame in a `published` notice carrying the `watchId`
// (host/PROTOCOL.md). A frame is `{value, ops}`; a `null` value retires the document or view.
import { withColdLoad } from "./coldload.ts"
import { replicatedState, type AttachedReplicatedState, type Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT, awaitWithContext } from "@earendil-works/chord/context"
import { applyImmutable, type Op } from "@earendil-works/chord/delta"
import type { CoreClient } from "../client.ts"
import { migrateRead } from "./tx.ts"
import { currentCommit } from "./commit-context.ts"
import { rememberSnapshot, revisionAt, sameContent, shareSnapshot } from "./snapshots.ts"
import { sharedChange } from "./publications.ts"
import { CommittedStateSource, CommittedWatch } from "./observation.ts"

/** What a watch observes. */
export type WatchTarget =
  | { kind: "doc"; address: unknown; def?: unknown }
  | { kind: "view"; conversationId: number; changes?: boolean; completing?: boolean }
  | { kind: "graph" }

/** `value` is absent in a view frame: the host applies `ops` to the value it holds. `changes` (view targets opened with `changes`) are the commit's entry, task and submission records of the conversation. */
export type Frame = { value?: unknown; ops: readonly Op[]; changes?: { entries: unknown[]; tasks: unknown[]; submissions: unknown[] } }

type Sink = {
  advance(value: unknown, ops: readonly Op[], context: Context): void
  /** Called for every frame of a view watch, after `advance`, with the value before and after (view.ts ViewObserver.publication). */
  publication?(before: unknown, after: unknown, ops: readonly Op[], changes: Frame["changes"], context: Context): void
  closeSession(failure?: { readonly error: unknown }): void
}

type Mount = { watchId: number; creating?: boolean; answeredMount?: boolean; request?: Promise<unknown>; result?: { value: unknown } | undefined; first?: unknown; completing?: readonly number[]; frames?: Frame[]; waiting: number; members: Set<Sink>; ready: Promise<{ value: unknown } | undefined> }

export class WatchHub {
  private nextId = 1
  private readonly sinks = new Map<number, Sink>()
  /** Frames that arrive between the core's answer and the moment the caller holds the watch. */
  private readonly early = new Map<number, Frame[]>()
  /** The value each open watch holds: a view frame carries ops only. */
  private readonly values = new Map<number, unknown>()
  private readonly targets = new Map<number, WatchTarget>()

  private closing = false
  private readonly mounts = new Map<string, Mount>()

  private readonly client: CoreClient
  private readonly report: (error: Error) => void
  private readonly context: Context

  constructor(client: CoreClient, report: (error: Error) => void, context: Context) {
    this.client = client; this.report = report; this.context = context
  }

  /** A frame the core published; `published` notices with a `watchId` end up here. */
  deliver(watchId: number, frame: Frame) {
    const sink = this.sinks.get(watchId)
    // The frame of a document watch repeats a change of the commit's publication: it holds that change's objects.
    const change = this.targets.get(watchId)?.kind === "doc" ? sharedChange(this.client, frame) : undefined
    if (change !== undefined) frame = { ...frame, ...(frame.value === undefined ? {} : { value: change.value }), ops: change.ops as never }
    if (!sink) { this.early.get(watchId)?.push(frame); return }
    this.apply(watchId, sink, frame, currentCommit(this.client) ?? this.context)
  }

  private apply(watchId: number, sink: Sink, frame: Frame, context: Context) {
    const before = this.values.get(watchId)
    const target = this.targets.get(watchId)
    // The frame of a commit this host wrote carries the revision its draft prepared (snapshots.ts Revision), as the Storage write and the publication do.
    const revision = target?.kind === "doc" ? revisionAt(this.client, target.address as never) : undefined
    const value = revision !== undefined && frame.value !== undefined && frame.value !== null && sameContent(frame.value, revision.value) ? revision.value : frame.value
    const ops = revision !== undefined && sameContent(frame.ops, revision.ops) ? revision.ops as never[] : frame.ops
    const after = value !== undefined ? value : ops.length > 0 ? applyImmutable(before as never, ops as never) : before
    this.values.set(watchId, after)
    if (target?.kind === "doc" && after !== undefined && after !== null ) rememberSnapshot(this.client, target.address, target.def as { kind?: unknown } | undefined, after)
    if (ops.length > 0 || value !== undefined) sink.advance(after, ops, context)
    sink.publication?.(before, after, ops, frame.changes, context)
  }

  /**
   * Open a watch. Resolves with the first value and a function that builds the consumer; frames queue until it does. A view
   * is one mount per conversation shared by its observers (view.ts ConversationViews): they hold the same revision objects.
   * An acquisition the caller cancels, or that the handle's close overtakes, rejects; a watch the core opened for it is closed.
   */
  private async open<T>(target: WatchTarget, context: Context, attach: (value: T, release: () => void, completing?: readonly number[]) => Sink): Promise<Sink | undefined> {
    context.abortSignal?.throwIfAborted()
    // A view is one mount per conversation and the task graph one mount for the Session, shared by their observers. Every observer of
    // a mount is advanced, then every observer is handed the publication (view.ts advance): a state and an event stream of one
    // conversation share the mount, so the state hears a commit before the stream does. The mount carries `changes` for the stream.
    if (target.kind === "view") target = { ...target, changes: true }
    const key = target.kind === "view" ? `${target.conversationId}` : target.kind === "graph" ? "graph" : undefined
    let mount = key === undefined ? undefined : this.mounts.get(key)
    let creator = false
    if (mount === undefined) {
      creator = true
      mount = { watchId: 0, waiting: 0, members: new Set(), ready: undefined as never }
      if (key !== undefined) this.mounts.set(key, mount)
      const owner = mount
      if (target.kind === "view") {
        // The mount's creator resumes on the core's answer itself, as `await this.attach(...)` resumes on readOnLine's run promise
        // (view.ts :100-113): the shared `ready` that later observers wait on settles from the same answer.
        owner.creating = true
        const request = owner.request = this.requestView(target, owner)
        owner.ready = request.then(first => this.answered<T>(target, owner, first as never), error => { this.early.delete(owner.watchId); throw error })
      } else owner.ready = this.acquire<T>(target, owner)
      owner.ready.then(first => { if (first === undefined && key !== undefined && this.mounts.get(key) === owner) this.mounts.delete(key) }, () => { if (key !== undefined && this.mounts.get(key) === owner) this.mounts.delete(key) })
    }
    const joined = mount
    joined.waiting++
    try {
      let value: { value: T } | undefined
      let joinedCompleting: readonly number[] | undefined
      if (creator && joined.request !== undefined) {
        await this.until(joined.request, context)
        value = joined.result as { value: T } | undefined
      } else {
        // An observer joining a view is its own job on the Session line, queued when it asks (view.ts attach), behind the job that builds
        // the mount; the job creates the observer, and the core reads the completing generations there for watchEvents.
        const attached = target.kind === "view" ? this.client.api<{ completing?: readonly number[] }>("view.attach", { conversationId: target.conversationId, completing: target.completing === true }) : undefined
        attached?.catch(() => {})
        // A mount that has answered is not awaited: Pi's attach goes straight to the Session line.
        value = joined.answeredMount ? joined.result as { value: T } | undefined : await this.until(joined.ready, context) as { value: T } | undefined
        if (value !== undefined && attached !== undefined) {
          // Commits between the build job and this observer's job publish while it waits. Frames of the answer's step that the
          // creator did not take (it gave up) are applied first, so this observer starts from the value at its own job (view.ts attach).
          if (!joined.creating) this.settleEarly(joined)
          joinedCompleting = (await this.until(attached, context)).completing
          context.abortSignal?.throwIfAborted()
        }
      }
      if (value === undefined) return undefined
      context.abortSignal?.throwIfAborted()
      // The acquisition ran on the line after close sealed admission: it rejects, as a queued Session job does.
      if (this.closing) throw new Error("Harness is closed")
      const frames = joined.frames
      joined.frames = undefined
      // The view's first observer reads the generations in `completing` on the same line job as the mount (events.ts :148-150).
      const completing = frames === undefined ? joinedCompleting : joined.completing
      joined.completing = undefined
      const member = attach((frames === undefined ? this.values.get(joined.watchId) : joined.first) as T, () => this.leave(key, joined, member), completing)
      joined.members.add(member)
      for (const f of frames ?? []) this.apply(joined.watchId, this.sinks.get(joined.watchId)!, f, this.context)
      return member
    } finally {
      joined.waiting--
      // An observer joining later takes the frames of the answer's step only once the creator can no longer take them.
      if (creator) joined.creating = false
      // Every caller that waited for this mount gave up: the core's watch has no observer.
      if (joined.waiting === 0 && joined.members.size === 0 && joined.watchId !== 0 && this.sinks.has(joined.watchId)) {
        if (key !== undefined && this.mounts.get(key) === joined) this.mounts.delete(key)
        this.release(joined.watchId)
      }
    }
  }

  /** Apply the frames of a mount's answer step that no observer took, so a later observer starts from the current value. */
  private settleEarly(mount: Mount) {
    const frames = mount.frames
    if (frames === undefined || !this.sinks.has(mount.watchId)) return
    mount.frames = undefined
    for (const f of frames) this.apply(mount.watchId, this.sinks.get(mount.watchId)!, f, this.context)
  }

  private release(watchId: number) {
    this.values.delete(watchId)
    this.targets.delete(watchId)
    if (!this.sinks.delete(watchId) || this.closing) return
    this.client.api("watch.close", { watchId }).catch(error => this.report(error as Error))
  }

  /** The first value of a mount, or undefined when the target is absent. The core's watch stays open while the mount has members. */
  private async acquire<T>(target: WatchTarget, mount: Mount): Promise<{ value: T } | undefined> {
    const watchId = mount.watchId = this.nextId++
    this.early.set(watchId, [])
    let first: { value: T | null; completing?: readonly number[] }
    try {
      const migrating = target.kind === "doc" ? (target.def as { migrate?: unknown } | undefined) ?? {} : {}
      const open = () => migrateRead(migrating, migrated => this.client.api<{ value: T | null; completing?: readonly number[] }>("watch.open", { watchId, target: migrated === undefined ? target : { ...target, migrated } }))
      first = await (target.kind === "doc" ? withColdLoad(this.client, target.address, BACKGROUND_CONTEXT, open) : open())
    } catch (error) {
      this.early.delete(watchId)
      throw error
    }
    return this.answered<T>(target, mount, first)
  }

  /** The core's watch.open of a view: its id, and the api promise of the one call. */
  private requestView(target: WatchTarget, mount: Mount) {
    const watchId = mount.watchId = this.nextId++
    this.early.set(watchId, [])
    return this.client.api<{ value: unknown; completing?: readonly number[] }>("watch.open", { watchId, target })
  }

  /** The core answered watch.open: the mount holds the first value and the frames of the answer's own step. */
  private answered<T>(target: WatchTarget, mount: Mount, first: { value: T | null; completing?: readonly number[] }): { value: T } | undefined {
    const watchId = mount.watchId
    mount.answeredMount = true
    const frames = this.early.get(watchId) ?? []
    this.early.delete(watchId)
    // Absent: nothing to observe. The core opened no watch to close.
    if (first.value === null) return mount.result = undefined
    if (target.kind === "doc") first.value = shareSnapshot(this.client, target.address, target.def as { kind?: unknown } | undefined, first.value) as T
    this.values.set(watchId, first.value)
    this.targets.set(watchId, target)
    this.sinks.set(watchId, this.fanOut(mount))
    // Frames of the answer's own step reach the first observer once it holds the first value.
    mount.first = first.value
    mount.completing = first.completing
    mount.frames = frames
    // Every caller gave up while the core answered: close the watch it opened for them.
    if (mount.waiting === 0) { for (const [k, m] of this.mounts) if (m === mount) this.mounts.delete(k); this.release(watchId) }
    return mount.result = { value: first.value }
  }

  private fanOut(mount: Mount): Sink {
    const each = (call: (sink: Sink) => void) => { for (const sink of [...mount.members]) { try { call(sink) } catch (error) { this.report(error as Error) } } }
    return {
      advance: (value, ops, context) => each(sink => sink.advance(value, ops, context)),
      publication: (before, after, ops, changes, context) => each(sink => sink.publication?.(before, after, ops, changes, context)),
      closeSession: failure => each(sink => sink.closeSession(failure)),
    }
  }

  /** Wait for `promise` unless the caller's context ends first. */
  private until<T>(promise: Promise<T>, context: Context): Promise<T> {
    return awaitWithContext(promise, context)
  }

  /** A member left its mount: the last one closes the core's watch. */
  private leave(key: string | undefined, mount: Mount, member: Sink) {
    if (!mount.members.delete(member) || mount.members.size > 0) return
    if (key !== undefined && this.mounts.get(key) === mount) this.mounts.delete(key)
    this.release(mount.watchId)
  }

  /** A serialized exact-frame watch, or undefined when the target is absent (upstream: `watchDoc` resolves undefined). */
  async watch<T>(target: WatchTarget, context: Context): Promise<CommittedWatch<T> | undefined> {
    let watch: CommittedWatch<T> | undefined
    await this.open<T>(target, context, (value, release) => {
      watch = new CommittedWatch<T>(value, release, error => this.report(error as Error))
      return watch as unknown as Sink
    })
    // A document watch ends as cancelled when its caller's context is cancelled later (session.ts watchDoc: observeCancellation).
    if (watch !== undefined && target.kind === "doc" && context.abortSignal !== undefined) watch.observeCancellation(context.abortSignal)
    return watch
  }

  /** Disposable read-only Chord state of the target, or undefined when absent. */
  async state<T>(target: WatchTarget, context: Context): Promise<AttachedReplicatedState<T> | undefined> {
    let source: CommittedStateSource<T> | undefined
    await this.open<T>(target, context, (value, release) => {
      source = new CommittedStateSource<T>(value, release)
      return source as unknown as Sink
    })
    return source ? replicatedState(source as never) as AttachedReplicatedState<T> : undefined
  }

  /** Attach a custom observer to a target (watchEvents): `make` receives the first value and the release function. Undefined when the target is absent. */
  async attach<T>(target: WatchTarget, context: Context, make: (value: T, release: () => void, completing?: readonly number[]) => Sink): Promise<Sink | undefined> {
    return this.open<T>(target, context, make)
  }

  /**
   * The host replaced the core handle: the watches the old core held are gone. Each established watch is opened again under its ID;
   * the commits that reached this host were all published before the replacement, so the first frame equals the value held and the
   * frames that follow continue it. A first frame that differs (the store changed under the host) replaces the value.
   */
  reattach() {
    if (this.closing) return
    for (const [watchId, target] of [...this.targets]) {
      const migrating = target.kind === "doc" ? (target.def as { migrate?: unknown } | undefined) ?? {} : {}
      migrateRead(migrating, migrated => this.client.api<{ value: unknown }>("watch.open", { watchId, target: migrated === undefined ? target : { ...target, migrated } })).then(first => {
        const sink = this.sinks.get(watchId)
        if (sink === undefined || first.value === null || first.value === undefined || sameContent(first.value, this.values.get(watchId))) return
        this.apply(watchId, sink, { value: first.value, ops: [] }, this.context)
      }, error => this.report(error as Error))
    }
  }

  /** The handle is closing: every watch and state ends with `session_closed` without a `watch.close` the core cannot answer. */
  closeSession(failure?: { readonly error: unknown }) {
    this.closing = true
    const sinks = [...this.sinks.values()]
    this.sinks.clear()
    this.mounts.clear()
    for (const sink of sinks) sink.closeSession(failure)
  }
}
