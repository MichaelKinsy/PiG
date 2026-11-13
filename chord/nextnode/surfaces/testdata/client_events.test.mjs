// Events (Pi 8d30e6306, decision "Events: fire-and-forget members") over Pi's own TypeScript host, vendored under client/pi and
// type-stripped by Node: an object's own event source reaches a reference's listener in order with the emit's stamp, an
// event member crosses a link (the `event` message and its ack, a hello without a call window between trusted ends), and the
// React bindings export `useEvents`. The shapes are those of Pi's test/node/fixtures/web (the page's beats) and of
// test/examples/67-events.ts. Run by surfaces_events_test.go (`node --test --conditions=source`).

import assert from "node:assert/strict";
import { createRequire, registerHooks } from "node:module";
import { describe, it } from "node:test";
import { setTimeout as pause } from "node:timers/promises";
import { BACKGROUND_CONTEXT as context } from "../client/pi/chord/src/context/index.ts";
import {
	createHost,
	defineFacet,
	event,
	hostKind,
	int,
	local,
	memoryLinkPair,
	num,
	obj,
	remote,
} from "../client/pi/chord/src/index.ts";

const Beats = local("test.beats", { version: 1, params: {}, members: { a: event(int()), b: event(int()) } });
const Server = hostKind("server");
const Sample = obj({ t: num(), y: num() });
const Game = remote("test.game", {
	version: 1,
	params: {},
	hostAddress: () => Server(),
	variants: { player: { events: { paddle: Sample } } },
});

/** Waits until `done()` holds, at most two seconds. */
async function until(done) {
	for (let turn = 0; turn < 400 && !done(); turn++) await pause(5);
	assert.ok(done(), "the condition never held");
}

describe("events of the vendored client", () => {
	it("deliver an own source's emits to a reference's listener in order, stamped, and to its member only", async () => {
		const errors = [];
		const host = createHost({ onError: (error) => errors.push(error) });
		let beat;
		await host.load(
			[
				{
					name: "@test/beats",
					facet: defineFacet((h) =>
						h.serve(Beats, (_params, self) => {
							const a = self.events();
							const b = self.events();
							beat = (member, value) => (member === "a" ? a : b).emit(value);
							return { a, b };
						}),
					),
				},
			],
			context,
		);
		const beats = await host.get(Beats, {}, context);
		const seen = [];
		const before = Date.now();
		const subscription = await beats.a.on(({ value, at }) => void seen.push([value, at >= before]), context);
		beat("a", 1);
		beat("b", 7);
		beat("a", 2);
		await until(() => seen.length === 2);
		assert.deepEqual(seen, [
			[1, true],
			[2, true],
		]);
		subscription.stop();
		beat("a", 3);
		await pause(20);
		assert.equal(seen.length, 2);
		assert.deepEqual(errors, []);
		await host.close(context);
	});

	it("cross a link: a client's reference gets the server's emits, value and stamp unchanged", async () => {
		const errors = [];
		const server = createHost({ address: Server(), principal: { kind: "server" }, onError: (error) => errors.push(error) });
		let paddle;
		await server.load(
			[
				{
					name: "@test/game",
					facet: defineFacet((h) =>
						h.serve(Game, (_params, _principal, self) => {
							paddle = self.events();
							return { variant: "player", members: { paddle } };
						}),
					),
				},
			],
			context,
		);
		const client = createHost({ onError: (error) => errors.push(error) });
		await client.load([], context);
		const [a, b] = memoryLinkPair();
		server.accept(a, { principal: { kind: "owner" } }, context);
		client.connect(b, { principal: { kind: "server" } }, context);
		const game = await client.get(Game, {}, context);
		const seen = [];
		await game.paddle.on(({ value, at }) => void seen.push({ value, at }), context);
		const before = Date.now();
		paddle.emit({ t: 1, y: 0.5 });
		paddle.emit({ t: 2, y: -0.25 });
		await until(() => seen.length === 2);
		const after = Date.now();
		assert.deepEqual(
			seen.map(({ value }) => value),
			[
				{ t: 1, y: 0.5 },
				{ t: 2, y: -0.25 },
			],
		);
		for (const { at } of seen) assert.ok(Number.isInteger(at) && at >= before && at <= after, `at = ${at}`);
		assert.deepEqual(errors, []);
		await client.close(context);
		await server.close(context);
	});

	it("are exported to React components as useEvents", async () => {
		// The vendored React is CommonJS whose index picks its build when it runs, so Node finds no named export in it:
		// `react` resolves here to a module that re-exports the vendored production build name by name.
		const from = new URL("../client/pi/chord/src/react/hooks.ts", import.meta.url).href;
		const names = Object.keys(createRequire(from)("react")).filter((name) => /^[A-Za-z_$][\w$]*$/.test(name));
		const source = [
			'import { createRequire } from "node:module";',
			`const react = createRequire(${JSON.stringify(from)})("react");`,
			...names.map((name) => `export const ${name} = react.${name};`),
		].join("\n");
		const hooks = registerHooks({
			resolve: (specifier, context, nextResolve) =>
				specifier === "react"
					? { url: `data:text/javascript,${encodeURIComponent(source)}`, shortCircuit: true }
					: nextResolve(specifier, context),
		});
		try {
			const react = await import("../client/pi/chord/src/react/index.ts");
			assert.equal(typeof react.useEvents, "function");
			assert.equal(react.useEvents.length, 2);
		} finally {
			hooks.deregister();
		}
	});
});
