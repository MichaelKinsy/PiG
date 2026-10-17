// Vitest setup file for the interleaving trace of U rows (CONTRACT section 5.8; cli/trace.mjs record-u / check-u). With
// CONTRACT_VITEST_TRACE=<file> every test of the run writes, to that file, a `test` marker, the API calls it makes on the Harness and
// on what the Harness returns (lib/apitrace.mjs), and each `commit(writes)` its storage receives: `commit` when the call starts and
// `commit-end` when it settles, with the microtask tick. The reference's ControlledStorage calls commit as Pi's Session commits;
// the candidate's core hands each commit to the same method before it applies it (PROTOCOL.md, "Storage gate"), so both sides give the same token.
import { appendFileSync, writeFileSync } from "node:fs"
import { Harness } from "@earendil-works/pi-durable"
import { afterEach, beforeEach, expect } from "vitest"
import { traceHarness } from "../lib/apitrace.mjs"
import { openTrace } from "../lib/trace.mjs"

const file = process.env.CONTRACT_VITEST_TRACE
if (file) {
	// The tick counts passes of the microtask queue while a test runs: the spin restarts with every test and stops after it.
	let passes = 0, generation = 0
	globalThis.__contractTick = () => passes
	// A test that waits for a macrotask (a timer) resumes once the bounded spin ends, as the E rows do.
	let limit = 0
	const spin = generationAtStart => { if (generationAtStart === generation && passes++ < limit) queueMicrotask(() => spin(generationAtStart)) }
	const sink = openTrace(`${file}.calls`)
	writeFileSync(file, "")
	const line = o => appendFileSync(file, JSON.stringify(o) + "\n")
	// apitrace emits through the trace sink: route it into the same file.
	sink.emit = o => line(o)
	const wrapStorage = storage => {
		if (!storage || storage.__traced) return storage
		// A plain MemoryStorage has no commit override on the candidate: give it one so the core reports each commit to it (PROTOCOL.md, "Storage gate").
		const original = typeof storage.commit === "function" ? storage.commit : () => undefined
		Object.defineProperty(storage, "__traced", { value: true })
		Object.defineProperty(storage, "commit", {
			configurable: true, writable: true,
			value(writes, ...rest) {
				const summary = [...new Set(writes.map(w => w.type))].sort().join(" ")
				line({ t: "storage", phase: "commit", summary, tick: globalThis.__contractTick() })
				const result = Reflect.apply(original, this, [writes, ...rest])
				const end = () => line({ t: "storage", phase: "commit-end", summary, tick: globalThis.__contractTick() })
				if (result && typeof result.then === "function") result.then(end, end)
				else end()
				return result
			},
		})
		return storage
	}
	const traced = traceHarness(Harness)
	const open = traced.open
	Object.defineProperty(Harness, "open", { configurable: true, writable: true, value: (storage, ...rest) => open(wrapStorage(storage), ...rest) })
	beforeEach(() => {
		line({ t: "test", name: expect.getState().currentTestName })
		const mine = ++generation
		limit = passes + 200_000
		queueMicrotask(() => spin(mine))
	})
	afterEach(() => { generation++ })
}
