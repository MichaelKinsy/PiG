// The fixture worker's JavaScript core facet (test/next-node/fixtures/workers/worker.ts `core`, with `Control` of control.ts), as the
// module the worker's Node host imports: `Control` (crash, busy, block, local calls) and `Reporter` (report, report and exit), the remote types
// served at the worker's address. `Busy` (the app's keep-alive counts) and `Echo` are served by the worker's Go core facet
// (workers_fixture_worker_test.go `fixtureGoCore`) and reach this host through its proxy facet (plugins.BridgedFacet): `Control` binds to
// them as it binds to any type of the closure.
import { existsSync } from "node:fs";
import { defineFacet, hostKind, int, local, map, method, remote, str } from "@earendil-works/chord";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";

const Session = hostKind("session", { id: str() });
const Server = hostKind("server");

const Control = remote("test.control", {
	version: 1,
	params: { id: str() },
	hostAddress: ({ id }) => Session({ id }),
	methods: { crash: method(int()), busy: method(int()), block: method(str()), localCalls: method(int(), int()) },
});

// What a worker reports to the server (control.ts `Reports`).
const Reports = remote("test.reports", {
	version: 1,
	params: {},
	hostAddress: () => Server(),
	methods: { report: method(str()) },
});

// Pi's `Control` also has `report` and `reportAndExit`, and its worker holds the server's `Reports` from the first report on
// (worker.ts `serverReports`). A module of this host holds a reference with its object (`self.get`, taken when the object activates),
// so a `Control` that had them would not be served without a server that serves `Reports`, and Pi's default fleet serves none: they
// are a type of their own at the same address.
const Reporter = remote("test.reporter", {
	version: 1,
	params: { id: str() },
	hostAddress: ({ id }) => Session({ id }),
	methods: { report: method(str()), reportAndExit: method(str()) },
});

const Busy = local("test.busy", { version: 1, params: {}, members: { counts: map(int()), set: method(int()), pulse: method(int()) } });
const Echo = local("test.echo", { version: 1, params: {}, members: { echo: method(int(), int()) } });

export default defineFacet((facet) => {
	facet.serve(Control, (_params, _principal, self) => {
		const busy = self.get(Busy, {});
		const echo = self.get(Echo, {});
		return {
			crash: async (code) => {
				setTimeout(() => process.exit(code), 10);
				return null;
			},
			// Busy for `ms`: the Go core facet sets the count now and clears it after the call settled (this object's references are
			// released with it, the Go object's timer is not).
			busy: async (ms, ctx) => {
				await busy.pulse(ms, ctx);
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
		};
	});
	facet.serve(Reporter, (_params, _principal, self) => {
		// The server's `Reports`, held with this object: a report goes out in the turn that sends it.
		const reports = self.get(Reports, {});
		return {
			// Sends the server a report and returns without waiting for its answer (the worker may be gone by then).
			report: async (text) => {
				reports.report(text, BACKGROUND_CONTEXT).catch(() => {});
				return null;
			},
			// Sends the server a report and exits (code 7) in the same turn.
			reportAndExit: async (text) => {
				reports.report(text, BACKGROUND_CONTEXT).catch(() => {});
				process.exit(7);
			},
		};
	});
});
