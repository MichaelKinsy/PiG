// detenv (CONTRACT section 7.2): remove nondeterminism at its source. Imported by hooks/register.mjs before any scenario
// module. The clock is virtual: it starts at a fixed epoch and moves only when a timer is due and nothing else can run, so
// the number of Date.now() calls cannot change a result (Pi and a candidate make different numbers of them). A virtual
// timer fires when the microtask queue is empty and no real I/O is pending (file operations, child processes, sockets);
// while real I/O is pending the driver polls every millisecond of real time. Math.random, crypto.getRandomValues and
// crypto.randomUUID are seeded (DETENV_SEED).
import { webcrypto } from "node:crypto"

const EPOCH = Number(process.env.DETENV_EPOCH ?? 1_800_000_000_000)
let now = EPOCH
let seed = Number(process.env.DETENV_SEED ?? 1) >>> 0
const next = () => { seed = (seed + 0x6d2b79f5) >>> 0; let t = seed; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296 }
const realSetTimeout = globalThis.setTimeout
const realSetImmediate = globalThis.setImmediate

Math.random = next
const fill = array => { for (let i = 0; i < array.length; i++) array[i] = Math.floor(next() * 256); return array }
const getRandomValues = array => { const b = new Uint8Array(array.buffer, array.byteOffset, array.byteLength); fill(b); return array }
const randomUUID = () => {
	const b = fill(new Uint8Array(16)); b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80
	const h = Array.from(b, x => x.toString(16).padStart(2, "0")).join("")
	return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}
for (const target of [webcrypto, globalThis.crypto]) for (const [name, value] of Object.entries({ getRandomValues, randomUUID })) Object.defineProperty(target, name, { value, configurable: true, writable: true })

const RealDate = Date
class VirtualDate extends RealDate {
	constructor(...args) { if (args.length === 0) super(now); else super(...args) }
	static now() { return now }
}
globalThis.Date = VirtualDate
const origin = performance.now()
performance.now = () => now - EPOCH + origin

/** Resources whose completion can still queue work: virtual time must not move while any exists. */
const REAL_IO = new Set(["FSReqCallback", "FSReqPromise", "ChildProcess", "TCPSocketWrap", "TCPConnectWrap", "GetAddrInfoReqWrap", "ProcessWrap", "CloseReq", "ShutdownWrap", "WriteWrap", "FSEvent", "Worker"])
// A child process is pending until its stdio has closed, not only until it has exited: its output is still in flight after
// exit, and the stdio pipes are indistinguishable from the process's own standard streams in getActiveResourcesInfo().
let children = 0
const realIoPending = () => children > 0 || process.getActiveResourcesInfo().some(name => REAL_IO.has(name))

let id = 1
const timers = new Map() // id -> {at, seq, fn, args, every}
let seq = 0
let driver = false
const schedule = () => {
	if (driver || !timers.size) return
	driver = true
	realSetImmediate(function tick() {
		if (realIoPending()) { realSetTimeout(tick, 1); return }
		const due = [...timers.entries()].sort((a, b) => a[1].at - b[1].at || a[1].seq - b[1].seq)[0]
		driver = false
		if (!due) return
		const [handle, t] = due
		if (process.env.DETENV_DEBUG && t.at - now > 1000) console.error("detenv: jump", t.at - now, process.getActiveResourcesInfo(), new Error().stack.split("\n").slice(1, 3).join(" | "))
		now = Math.max(now, t.at)
		if (t.every !== undefined) { t.at = now + Math.max(1, t.every); t.seq = seq++ } else timers.delete(handle)
		t.fn(...t.args)
		schedule()
	})
}
const make = every => (fn, ms = 0, ...args) => {
	const handle = id++
	timers.set(handle, { at: now + Math.max(0, Number(ms) || 0), seq: seq++, fn, args, every: every ? Math.max(1, Number(ms) || 1) : undefined })
	schedule()
	const t = { ref() { return t }, unref() { return t }, hasRef: () => true, refresh() { return t }, [Symbol.toPrimitive]: () => handle, _handle: handle }
	return t
}
const clear = t => { timers.delete(typeof t === "object" && t ? t._handle : Number(t)) }
globalThis.setTimeout = make(false)
globalThis.setInterval = make(true)
globalThis.clearTimeout = clear
globalThis.clearInterval = clear

/** The virtual clock, for `HarnessOptions.now` and the tape. */
export const clock = { now: () => now, epoch: EPOCH, pending: () => timers.size }
globalThis[Symbol.for("pig.contract.clock")] = clock

// mkdtemp draws its random suffix from the operating system. The suffix becomes a deterministic hash of the seed, the prefix
// and the number of earlier calls with that prefix, so it does not depend on how many other random numbers were drawn.
import { createRequire, syncBuiltinESMExports } from "node:module"
import { createHash } from "node:crypto"
const require = createRequire(import.meta.url)
const fs = require("node:fs")
const ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const counts = new Map()
const suffix = prefix => {
	const n = counts.get(prefix) ?? 0
	counts.set(prefix, n + 1)
	const h = createHash("sha256").update(`${process.env.DETENV_SEED ?? 1}\0${prefix}\0${n}`).digest()
	return Array.from({ length: 6 }, (_, i) => ALPHABET[h[i] % ALPHABET.length]).join("")
}
const mkdtempSync = fs.mkdtempSync
fs.mkdtempSync = (prefix, options) => {
	const path = `${prefix}${suffix(prefix)}`
	fs.mkdirSync(path)
	return options?.encoding === "buffer" ? Buffer.from(path) : path
}
fs.mkdtemp = (prefix, options, callback) => {
	if (typeof options === "function") { callback = options; options = undefined }
	try { const path = fs.mkdtempSync(prefix, options); queueMicrotask(() => callback(null, path)) } catch (e) { queueMicrotask(() => callback(e)) }
}
fs.promises.mkdtemp = async (prefix, options) => fs.mkdtempSync(prefix, options)
void mkdtempSync
syncBuiltinESMExports()

const cp = require("node:child_process")
for (const name of ["spawn", "exec", "execFile", "fork"]) {
	const original = cp[name]
	cp[name] = function (...args) {
		const child = original.apply(this, args)
		children++
		let done = false
		const end = () => { if (!done) { done = true; children-- } }
		child.once("close", end)
		child.once("error", end)
		return child
	}
}
syncBuiltinESMExports()
