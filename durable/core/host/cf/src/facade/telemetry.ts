// The host side of Durable's telemetry (src/harness/telemetry.ts DURABLE_TELEMETRY_SCHEMA; its use in harness/scheduler.ts, harness/submissions.ts
// and session/session.ts, Pi 2ec55d80 "Durable and pi-ai telemetry"). Spans are JavaScript objects, so the core never holds one: it names them
// by the handles a SpanTable assigns, and tells the host what to record in `telemetry` notices (core/telemetry.go lists the ops). A call whose
// Context carries a span gives the core its handle (`span`) and the span's carrier (`trace`); the core answers a phase's start with the
// `phaseSpan` hook, whose answer is the span's handle and carrier.
import { BACKGROUND_CONTEXT, withTelemetry } from "@earendil-works/chord/context"
import type { Context } from "@earendil-works/chord"
import { NOOP_TELEMETRY_CONTEXT, NOOP_TELEMETRY_METRICS, type SpanAttributes, type TelemetryCarrier, type TelemetryContext, type TelemetrySpan } from "@earendil-works/pi-telemetry"
import type { CoreClient } from "../client.ts"
import type { ReportInfo } from "@earendil-works/pi-durable"

/** The spans the core names, by handle. A phase's span is held until it ends; a span a caller's Context carries is held weakly: the caller holds it for the call. */
export class SpanTable {
  private next = 1
  private readonly byHandle = new Map<number, WeakRef<object> | object>()
  private readonly handles = new WeakMap<object, number>()
  private assigned = 0

  /** The handle of a span, 0 for the NOOP span (it takes no event). */
  handleOf(span: TelemetryContext | undefined): number {
    if (span === undefined || span === NOOP_TELEMETRY_CONTEXT) return 0
    let handle = this.handles.get(span)
    if (handle === undefined) {
      handle = this.next++
      this.handles.set(span, handle)
      this.byHandle.set(handle, new WeakRef(span))
      if (++this.assigned % 1024 === 0) this.prune()
    }
    return handle
  }

  /** Hold a span strongly until `release`: its caller is the core, not a function that stays on the stack. */
  hold(span: TelemetryContext): number {
    const handle = this.handleOf(span)
    this.byHandle.set(handle, span)
    return handle
  }

  release(handle: number): void {
    const held = this.byHandle.get(handle)
    if (held !== undefined && !(held instanceof WeakRef)) this.byHandle.set(handle, new WeakRef(held))
  }

  span(handle: number | undefined): TelemetrySpan | undefined {
    if (handle === undefined || handle === 0) return undefined
    const held = this.byHandle.get(handle)
    return (held instanceof WeakRef ? held.deref() : held) as TelemetrySpan | undefined
  }

  private prune(): void {
    for (const [handle, held] of this.byHandle) if (held instanceof WeakRef && held.deref() === undefined) this.byHandle.delete(handle)
  }
}

const tables = new WeakMap<CoreClient, SpanTable>()
/** The span table of a client's session. */
export function spansOf(client: CoreClient): SpanTable {
  let table = tables.get(client)
  if (table === undefined) tables.set(client, table = new SpanTable())
  return table
}

/** The fields a call gives the core for its Context's span: its handle and carrier; none for the NOOP span. */
export function spanFields(client: CoreClient, context: Context): { span?: number; trace?: TelemetryCarrier } {
  const telemetry = context.telemetry
  const span = spansOf(client).handleOf(telemetry)
  if (span === 0) return {}
  const trace = telemetry.propagation?.inject(telemetry)
  return trace === undefined ? { span } : { span, trace }
}

const TELEMETRY: ReportInfo = { type: "host_error", callback: "telemetry" }
const ROOT = NOOP_TELEMETRY_CONTEXT as TelemetryContext

type Open = { end: () => void; started: number; task: string; phase: string }

/** A Harness's telemetry: HarnessOptions.telemetry's spans and metrics, driven by the core's notices and hooks. */
export class HarnessTelemetry {
  readonly root: TelemetryContext
  readonly table: SpanTable
  private readonly open = new Map<number, Open>()
  private readonly reports
  private readonly active
  private readonly duration
  private readonly report: (error: unknown, info: ReportInfo, context?: Context) => void

  constructor(root: TelemetryContext | undefined, table: SpanTable, report: (error: unknown, info: ReportInfo, context?: Context) => void) {
    this.root = root ?? ROOT
    this.table = table
    this.report = report
    const metrics = this.root.metrics ?? NOOP_TELEMETRY_METRICS
    this.reports = metrics.counter("pi.durable.reports", { description: "Reports raised, grouped by their fixed ReportInfo type." })
    this.active = metrics.upDownCounter("pi.durable.tasks.active", { description: "Live tasks held by this Harness; decremented at terminal settlement or close." })
    this.duration = metrics.histogram("pi.durable.phase.duration", { description: "Duration of a phase invocation and its decision commit.", unit: "ms" })
  }

  /** The Context of a host effect under the span of a phase, by handle. */
  spanContext = (handle: number, base: Context): Context => {
    const span = this.table.span(handle)
    return span === undefined ? base : withTelemetry(span, base)
  }

  /** submissions.ts submit: the `pi.durable.submit` span around one admission. `send` makes the call to the core with the span's fields; `undefined` when the span is the NOOP span. */
  submit<T extends { id: number; status?: string; link?: TelemetryCarrier }>(conversationId: number, draft: { type: string }, context: Context, send: (fields: Record<string, unknown>) => Promise<T>): Promise<T> | undefined {
    // Inputs start their own trace, linked to the caller; writes are children of the caller's span.
    const parent = draft.type === "input" ? this.root : context.telemetry
    if (parent === ROOT) return undefined
    return parent.startSpan({ name: "pi.durable.submit", attributes: { "submission.type": draft.type, "conversation.id": conversationId } }, async span => {
      if (draft.type === "input") span.addLink({ context: context.telemetry })
      const handle = this.table.hold(span)
      try {
        const trace = span.propagation?.inject(span)
        const placed = await send({ span: handle, ...(trace === undefined ? {} : { trace }), ...(span.propagation === undefined ? {} : { propagates: true }) })
        if (placed.link !== undefined && span.propagation !== undefined) span.addLink({ context: span.propagation.extract(placed.link) })
        span.setAttributes({ "submission.id": placed.id, result: placed.status })
        return placed
      } catch (error) {
        // Error text may contain caller content: record only the status.
        span.setStatus({ status: "error" })
        throw error
      } finally {
        this.table.release(handle)
      }
    })
  }

  /** The `phaseSpan` hook: start the span of a phase invocation and answer its handle and carrier. */
  phaseSpan = (p: { task: number; name: string; phase: string; abort?: boolean; resumed?: boolean; attempt?: number; trace?: TelemetryCarrier; links?: TelemetryCarrier[] }): Promise<{ span: number; carrier?: TelemetryCarrier }> => {
    const parent = p.trace === undefined ? this.root : (this.root.propagation?.extract(p.trace) ?? this.root)
    const answer = Promise.withResolvers<{ span: number; carrier?: TelemetryCarrier }>()
    const { resolve } = answer
    {
      parent.startSpan({
        name: "pi.durable.phase",
        attributes: { "task.id": p.task, "task.name": p.name, phase: p.phase, kind: p.abort === true ? "abort" : "run", resumed: p.resumed === true, ...(p.attempt === undefined ? {} : { attempt: p.attempt }) },
      }, span => {
        if (span.propagation !== undefined && p.links !== undefined) for (const carrier of p.links) span.addLink({ context: span.propagation.extract(carrier) })
        const handle = this.table.hold(span)
        const ending = Promise.withResolvers<void>()
        this.open.set(handle, { end: ending.resolve, started: performance.now(), task: p.name, phase: p.phase })
        const ended = ending.promise
        const carrier = span.propagation?.inject(span)
        resolve(carrier === undefined ? { span: handle } : { span: handle, carrier })
        return ended
      }).catch(() => resolve({ span: 0 }))
    }
    return answer.promise
  }

  /** End every phase span still open: the core was discarded or closed with invocations in flight. */
  endAll(): void {
    for (const [handle, open] of [...this.open]) { this.open.delete(handle); this.table.release(handle); open.end() }
  }

  /** One `telemetry` notice. A throwing adapter is reported, as the commit that raised the event stands. */
  notice(op: { op: string; span?: number; [key: string]: unknown }): void {
    const span = this.table.span(op.span)
    try {
      switch (op.op) {
        case "entry": span?.addEvent("pi.durable.entry", { "entry.id": op.id as number, "entry.kind": op.kind as string }); break
        case "submission": span?.addEvent("pi.durable.submission", { "submission.id": op.id as number, status: op.status as string, "answer.entry_id": op.answer as number | undefined }); break
        case "checkpoint": span?.addEvent("pi.durable.checkpoint", { phase: op.phase as string }); break
        case "replay": span?.addEvent("pi.durable.replay", { "tool.name": op.tool as string }); break
        case "tool": span?.setAttributes({ "tool.name": op.name as string, "tool.call_id": op.callId as string }); break
        case "end": this.end(op as never); break
        case "active": this.active.add(op.delta as number, { task: op.task as string }); break
      }
    } catch (error) {
      this.report(error, TELEMETRY, span === undefined ? undefined : withTelemetry(span, BACKGROUND_CONTEXT))
    }
  }

  private end(op: { span: number; outcome: string; next?: string; ended?: string; message?: string }): void {
    const open = this.open.get(op.span)
    const span = this.table.span(op.span)
    this.open.delete(op.span)
    if (open === undefined || span === undefined) return
    const outcome = op.outcome === "next" ? `next=${op.next}` : op.outcome
    try {
      span.setAttributes({ outcome })
      if (op.outcome === "faulted") span.setStatus({ status: "error", error: { name: `task_${op.ended}`, message: (op.message ?? "").slice(0, 500) } })
      else if (op.outcome === "aborted") span.setStatus({ status: "ok" })
      this.duration.record(performance.now() - open.started, { task: open.task, phase: open.phase, outcome }, span)
    } catch (error) {
      this.report(error, TELEMETRY, withTelemetry(span, BACKGROUND_CONTEXT))
    } finally {
      this.table.release(op.span)
      open.end()
    }
  }

  /** harness.ts safeReport, after `onReport`: count the report by its type and record its fixed fields on the span of `context`. A throwing metric or event is dropped. */
  reported(info: ReportInfo, context: Context | undefined): void {
    const attributes: SpanAttributes = { type: info.type }
    const i = info as Record<string, any>
    switch (info.type) {
      case "task_faulted": case "task_outdated": attributes.reason = i.reason; attributes.extension = i.extension; attributes["task.id"] = i.task?.id; break
      case "task_parked": case "migration_failed": case "task_error": case "extension_error": attributes.extension = i.extension; attributes["task.id"] = i.task?.id; break
      case "host_error": attributes["task.id"] = i.task?.id; break
    }
    try {
      this.reports.add(1, { type: info.type })
      context?.telemetry.addEvent("pi.durable.report", attributes)
    } catch { /* dropped: reporting it would report again */ }
  }
}
