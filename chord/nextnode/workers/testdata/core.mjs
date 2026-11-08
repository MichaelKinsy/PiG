// The fixture worker's JavaScript core facet (test/next-node/fixtures/workers/worker.ts `core`, with `Control` of control.ts), as the
// module the worker's Node host imports: `Control` (crash, busy, local calls), the remote type served at the worker's address. `Busy` (the
// app's keep-alive counts) and `Echo` are served by the worker's Go core facet (workers_fixture_worker_test.go `fixtureGoCore`) and
// reach this host through its proxy facet (plugins.BridgedFacet): `Control` binds to them as it binds to any type of the closure.
import { existsSync } from "node:fs";
import { defineFacet, hostKind, int, local, map, method, remote, str } from "@earendil-works/chord";

const Session = hostKind("session", { id: str() });

const Control = remote("test.control", {
	version: 1,
	params: { id: str() },
	hostAddress: ({ id }) => Session({ id }),
	variants: { main: { methods: { crash: method(int()), busy: method(int()), block: method(str()), localCalls: method(int(), int()) } } },
});

const Busy = local("test.busy", { version: 1, params: {}, members: { counts: map(int()), set: method(int()), pulse: method(int()) } });
const Echo = local("test.echo", { version: 1, params: {}, members: { echo: method(int(), int()) } });

export default defineFacet((facet) => {
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
			},
		};
	});
});
