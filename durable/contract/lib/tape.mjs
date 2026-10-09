// The effect tape (CONTRACT sections 5.3 and 7.3): model responses, tool results and generated ids are inputs. Record mode
// (the pi-durable capture) stores, per effect, its request (context fingerprint) and its completion (events, result, and
// the virtual time of each); replay mode serves them to a candidate and fails on the first request that differs from the
// recorded one. Each effect is a JSON line; effects of one kind replay in recorded order, so a mismatch is the first
// divergent call. The tape is keyed by kind only because the hooks sit at one place per kind.
import { appendFileSync, readFileSync, writeFileSync } from "node:fs"
import { AsyncLocalStorage } from "node:async_hooks"
import { ctxFingerprint } from "./canon.mjs"
import { emit } from "./trace.mjs"

const KEY = Symbol.for("pig.contract.tape")
const clock = () => globalThis[Symbol.for("pig.contract.clock")]
const now = () => clock()?.now() ?? Date.now()

export class TapeMismatch extends Error {
	constructor(message, detail) { super(message); this.name = "TapeMismatch"; this.detail = detail }
}

/** The process-wide tape, configured from CONTRACT_TAPE and CONTRACT_TAPE_MODE (default record). */
export function tape() {
	if (globalThis[KEY]) return globalThis[KEY]
	const path = process.env.CONTRACT_TAPE
	const mode = path ? (process.env.CONTRACT_TAPE_MODE ?? "record") : "off"
	const t = { mode, path, entries: [], cursor: new Map() }
	if (mode === "record") writeFileSync(path, "")
	if (mode === "replay") t.entries = readFileSync(path, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
	globalThis[KEY] = t
	return t
}

/** Record an effect completed with `value`; in replay mode return the next recorded effect of this kind (or throw). */
function nextRecorded(t, kind, request) {
	const i = t.cursor.get(kind) ?? 0
	const recorded = t.entries.filter(e => e.k === kind)[i]
	t.cursor.set(kind, i + 1)
	if (!recorded) throw new TapeMismatch(`tape: ${kind} #${i} was not recorded`, { kind, index: i, request })
	if (request !== undefined && JSON.stringify(recorded.request) !== JSON.stringify(request)) {
		emit({ t: "mismatch", kind, index: i, recorded: recorded.request, got: request })
		throw new TapeMismatch(`tape: ${kind} #${i} request differs from the recording`, { kind, index: i, recorded: recorded.request, got: request })
	}
	return recorded
}
const record = (t, entry) => appendFileSync(t.path, JSON.stringify(entry) + "\n")

const depth = new AsyncLocalStorage()

/** Wrap a pi-ai Models object: streamSimple, completeSimple, fetchDeferred and cancelDeferred go through the tape. */
export function wrapModels(models, { createStream }) {
	const t = tape()
	let call = 0
	const fingerprint = (model, context, options) => ({ model: { provider: model?.provider, id: model?.id }, ctx: ctxFingerprint(context, options) })

	const streamSimple = models.streamSimple.bind(models)
	models.streamSimple = (model, context, options) => {
		if (depth.getStore()) return streamSimple(model, context, options)
		const request = fingerprint(model, context, options)
		const n = ++call
		// A scenario program may add durable-bench's own fingerprint of the call (CONTRACT section 5.4).
		const benchfp = globalThis[Symbol.for("pig.contract.benchfp")]
		emit({ t: "model", call: n, ...request, ...(typeof benchfp === "function" ? { bench: benchfp(context.messages) } : {}), ...(typeof context.messages?.length === "number" ? { messages: context.messages.length } : {}) })
		if (t.mode === "replay") {
			const recorded = nextRecorded(t, "model", request)
			const stream = createStream()
			void (async () => {
				for (const { at, event } of recorded.events) {
					const wait = recorded.base + at - now()
					if (wait > 0) await new Promise(resolve => setTimeout(resolve, wait))
					stream.push(event)
				}
			})()
			return stream
		}
		const stream = streamSimple(model, context, options)
		if (t.mode === "record") {
			const started = now()
			const events = []
			const push = stream.push.bind(stream)
			stream.push = event => { events.push({ at: now() - started, event: structuredClone(event) }); push(event) }
			void stream.result().then(() => record(t, { k: "model", request, base: started, events }), () => record(t, { k: "model", request, base: started, events, failed: true }))
		}
		return stream
	}

	for (const name of ["completeSimple", "fetchDeferred", "cancelDeferred"]) {
		const original = models[name]?.bind(models)
		if (!original) continue
		models[name] = async (model, ...args) => {
			if (depth.getStore()) return original(model, ...args)
			const request = { op: name, model: { provider: model?.provider, id: model?.id }, args: name === "completeSimple" ? ctxFingerprint(args[0], args[1]) : JSON.stringify(args[0]) }
			emit({ t: "effect", op: name, ...request })
			if (t.mode === "replay") return nextRecorded(t, name, request).value
			const value = await depth.run(true, () => original(model, ...args))
			if (t.mode === "record") record(t, { k: name, request, value })
			return value
		}
	}
	return models
}

/** Wrap uuidv7(): record each value, or return the recorded one (ABI section 4.2: `pig.uuidv7`). */
export function wrapUuid(real) {
	const t = tape()
	return (...args) => {
		const value = t.mode === "replay" ? nextRecorded(t, "uuid").value : real(...args)
		emit({ t: "uuid", value })
		if (t.mode === "record") record(t, { k: "uuid", value })
		return value
	}
}
