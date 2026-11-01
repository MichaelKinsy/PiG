// The fixture worker's core facet (test/next-node/fixtures/workers/worker.ts `core`, with `Control` of control.ts), as the module
// the worker's Node host imports: `Control` (crash, busy, block, local calls), `Busy` (the app's keep-alive counts) and `Echo`. The
// worker's host is the NodeHost, so the app's Go code reads the counts through `BusyView`, a remote type at the address.
import { existsSync } from "node:fs";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { defineFacet, defineState, hostKind, int, local, map, method, remote, str } from "@earendil-works/chord/next";

const Session = hostKind("session", { id: str() });

const Control = remote("test.control", {
	version: 1,
	params: { id: str() },
	hostAddress: ({ id }) => Session({ id }),
	variants: { main: { methods: { crash: method(int()), busy: method(int()), block: method(str()), localCalls: method(int(), int()) } } },
});

/** The counts of Busy, read by the worker's Go code (a local type of this host cannot be read from Go). */
const BusyView = remote("test.busyview", {
	version: 1,
	params: { id: str() },
	hostAddress: ({ id }) => Session({ id }),
	variants: { main: { state: { counts: map(int()) } } },
});

const Busy = local("test.busy", { version: 1, params: {}, members: { counts: map(int()), set: method(int()) } });
const Echo = local("test.echo", { version: 1, params: {}, members: { echo: method(int(), int()) } });
const Counts = defineState("counts", { schema: map(int()), version: 1, initial: () => ({ tasks: 0 }) });

/** Busy's document while its object lives (the worker's Go code holds BusyView, which holds Busy, for the process's life). */
let counts;

export default defineFacet((facet) => {
	facet.serve(Busy, (_, self) => {
		const doc = self.state(Counts);
		counts = doc;
		return {
			counts: self.derive([doc], ([value]) => value),
			set: async (n, ctx) => {
				await doc.change((draft) => {
					draft.tasks = n;
				}, ctx);
				return null;
			},
		};
	});
	facet.serve(Echo, () => ({ echo: async (n) => n }));
	facet.serve(BusyView, (_params, _principal, self) => {
		const busy = self.get(Busy, {});
		return { variant: "main", members: { counts: self.derive([busy.counts], ([value]) => value) } };
	});
	facet.serve(Control, (_params, _principal, self) => {
		const busy = self.get(Busy, {});
		const echo = self.get(Echo, {});
		return {
			variant: "main",
			members: {
				crash: async (code) => {
					setTimeout(() => process.exit(code), 10);
					return null;
				},
				busy: async (ms, ctx) => {
					await busy.set(1, ctx);
					// After the call settled (its ctx sealed, maybe this object ended): through the document of the Busy object.
					setTimeout(async () => {
						await counts.change((draft) => {
							draft.tasks = 0;
						}, BACKGROUND_CONTEXT);
					}, ms);
					return null;
				},
				// Blocks this host's event loop (Pi's `block`, a busy loop) until the file `release` exists, for 2 s at most: nothing
				// the process is sent (an abort, a close) is read meanwhile. The call settles first; `node blocked` is printed once
				// the loop is blocked, in the turn that blocks it.
				block: async (release) => {
					setTimeout(() => {
						console.log("node blocked");
						const cell = new Int32Array(new SharedArrayBuffer(4));
						for (const end = Date.now() + 2000; !existsSync(release) && Date.now() < end; ) Atomics.wait(cell, 0, 0, 5);
					}, 0);
					return null;
				},
				// The worker's Go code counts the messages it sends to the server between the two lines.
				localCalls: async (n, ctx) => {
					console.log("local calls begin");
					for (let i = 0; i < n; i++) await echo.echo(i, ctx);
					console.log("local calls end");
					return n;
				},
			},
		};
	});
});
