// The API trace (CONTRACT section 5.8): with CONTRACT_API_TRACE=1 every call a scenario makes on the Harness and on the objects it
// returns (Conversation, Submission, task handles...) writes `call` lines to the run's trace: `enter` when the call is made and
// `return` when it settles, each with the microtask tick (CONTRACT_TICKS). Next to the commit lines they give the order in which
// client calls and commits interleave, which is what Pi's microtask scheduling decides and what a candidate must reproduce
// exactly (cli/trace.mjs). The methods are wrapped in place on the real object, so identity, private fields and the library's own
// WeakMap lookups (conversationViews(harness)) are untouched.
import { resolve } from "node:path"
import { pathToFileURL } from "node:url"
import { emit } from "./trace.mjs"

const SKIP = new Set(["then", "catch", "finally", "constructor", "toJSON", "inspect", "toString", "valueOf"])
const plain = v => v === null || typeof v !== "object" || Array.isArray(v) || v instanceof Promise || Object.getPrototypeOf(v) === Object.prototype || v instanceof Uint8Array || v instanceof Map || v instanceof Set || v instanceof Date || v instanceof Error
const tick = () => globalThis.__contractTick?.()
const done = new WeakSet()
// A call is the scenario's when the first frame outside this file is not library code (the reference's src, the candidate's facade, node itself):
// methods the library calls on itself also run through the wrappers, and Pi's internals are not the contract.
// The candidate's directory (CONTRACT_CANDIDATE) is library code wherever it lives.
const INTERNAL = /\/packages\/[\w-]+\/src\/|\/host\/cf\/src\/|\/host\/cf\/candidate\/|^node:|\/node_modules\//
const candidate = process.env.CONTRACT_CANDIDATE ? resolve(process.env.CONTRACT_CANDIDATE) : undefined
const candidateUrl = candidate && pathToFileURL(candidate).href
const fromScenario = () => {
	const frames = new Error().stack.split("\n").slice(2)
	const frame = frames.find(f => !f.includes("apitrace.mjs"))
	if (!frame) return true
	const file = ((/\(?((?:file:\/\/)?[^()]+?):\d+:\d+\)?$/.exec(frame.trim()) ?? [])[1] ?? frame).replace(/^at /, "")
	if (candidate && (file.startsWith(`${candidateUrl}/`) || file.startsWith(`${candidate}/`))) return false
	return !INTERNAL.test(file)
}
let serial = 0

const methodsOf = object => {
	const names = new Set()
	// A function stored on the instance (an arrow function property) is a method too.
	for (const name of Object.getOwnPropertyNames(object)) if (typeof Object.getOwnPropertyDescriptor(object, name)?.value === "function" && !SKIP.has(name)) names.add(name)
	for (let o = Object.getPrototypeOf(object); o && o !== Object.prototype; o = Object.getPrototypeOf(o)) {
		for (const name of Object.getOwnPropertyNames(o)) {
			const d = Object.getOwnPropertyDescriptor(o, name)
			if (typeof d?.value === "function" && !SKIP.has(name)) names.add(name)
		}
	}
	return names
}

/** Wrap the methods of `object` (and, as they are returned, of the objects they return) to trace their calls; returns the object. */
export function traced(object, label) {
	if (plain(object) || typeof object === "function" || done.has(object) || !Object.isExtensible(object)) return object
	done.add(object)
	for (const name of methodsOf(object)) {
		const original = object[name]
		const callName = `${label}.${name}`
		Object.defineProperty(object, name, {
			configurable: true, writable: true, enumerable: false,
			value(...args) {
				const mine = fromScenario()
				const id = ++serial
				if (mine) emit({ t: "call", phase: "enter", id, name: callName, tick: tick() })
				const finish = result => { if (mine) emit({ t: "call", phase: "return", id, name: callName, tick: tick() }); return mine ? traced(result, `${callName}()`) : result }
				const fail = error => { if (mine) emit({ t: "call", phase: "throw", id, name: callName, tick: tick() }) }
				let result
				try { result = Reflect.apply(original, this, args) } catch (error) { fail(error); throw error }
				// The caller keeps the original promise: an observer added to it costs the caller no microtask hop, so tracing does not move Pi's schedule.
				if (result && typeof result.then === "function") { result.then(finish, fail); return result }
				return finish(result)
			},
		})
	}
	return object
}

/** The Harness class with `open` returning a traced Harness. */
export function traceHarness(Harness) {
	if (!Harness || typeof Harness.open !== "function") return Harness
	return new Proxy(Harness, {
		get(object, property) {
			const value = Reflect.get(object, property, object)
			if (property !== "open") return value
			// The wrapper returns open's own promise and wraps the Harness in a reaction registered before the caller's: an
			// async wrapper would add two microtask hops to every later tick of the run.
			return (...args) => {
				const opened = Reflect.apply(value, object, args)
				if (!opened || typeof opened.then !== "function") return traced(opened, "harness")
				opened.then(harness => traced(harness, "harness"), () => {})
				return opened
			}
		},
	})
}
