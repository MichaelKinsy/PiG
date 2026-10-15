// The `attach` of upstream harness/view.ts `conversationViews(harness)`, for the Pi tests that attach a view observer. Partial: no `state` or
// `watch`, and each call builds its own instance where Pi keeps one per Harness. The core mounts the view.
// The core mounts the view (a `view` watch with changes); this attaches one observer to it. An observer that throws is reported through
// the Harness and the others still get the frame; the Session's close reaches `closeSession` once.
import type { Context } from "@earendil-works/chord"
import { Harness } from "../harness.ts"

type Observer = {
  advance?(value: unknown, ops: readonly unknown[], context: Context): void
  publication?(before: unknown, after: unknown, ops: readonly unknown[], publication: unknown, context: Context): void
  closeSession(failure: { readonly error: unknown } | undefined): void
}

export class ConversationViews {
  readonly #harness: Harness
  constructor(harness: Harness) { this.#harness = harness }

  /** Register an observer created from the current revision: it sees every later publication and nothing earlier. */
  async attach<O extends Observer>(
    id: number,
    create: (value: unknown, release: () => void, storage: unknown) => O | Promise<O>,
    context: Context,
  ): Promise<{ observer: O; detach: () => void }> {
    let observer: O | undefined
    const made = Promise.withResolvers<O>()
    let detach!: () => void
    const early: ((observer: O) => void)[] = []
    const deliver = (call: (observer: O) => void) => { if (observer !== undefined) call(observer); else early.push(call) }
    const ready = await this.#harness.attachView(id, context, (value: unknown, release: () => void) => {
      detach = release
      void Promise.resolve(create(value, release, this.#harness.storageOf())).then(created => {
        observer = created
        made.resolve(created)
        for (const call of early.splice(0)) { try { call(created) } catch (error) { this.#harness.report(error) } }
      }, error => made.reject(error))
      return {
        advance: (next, ops, frameContext) => deliver(o => o.advance?.(next, ops, frameContext)),
        publication: (before, after, ops, changes, frameContext) => deliver(o => o.publication?.(before, after, ops, { changes }, frameContext)),
        closeSession: failure => deliver(o => o.closeSession(failure)),
      }
    })
    if (ready === undefined) throw new Error(`Conversation ${id} does not exist`)
    return { observer: await made.promise, detach }
  }
}

/** The view mounts of a Harness. */
export function conversationViews(harness: object): ConversationViews {
  if (!(harness instanceof Harness)) throw new Error("Not a Harness")
  return new ConversationViews(harness)
}
