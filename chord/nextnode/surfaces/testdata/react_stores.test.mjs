// Pi's test/node/react/stores.test.ts (Pi main c23b9ec1) for node:test: the external stores the React bindings read (slice 11 §3),
// without React, over Pi's own TypeScript host (vendored under client/pi, type-stripped by Node). A store's snapshot is the
// watchable's value (`undefined` when it has none, never a throw), and subscribing calls back on every change until unsubscribed.
// Run by react_stores_test.go (`node --test --conditions=source`); vitest's `expect` is node:assert.

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { setTimeout as pause } from "node:timers/promises";
import { BACKGROUND_CONTEXT as context } from "../client/pi/chord/src/context/index.ts";
import {
	constant,
	createHost,
	defineFacet,
	hostKind,
	local,
	memoryLinkPair,
	method,
	remote,
	str,
} from "../client/pi/chord/src/index.ts";
import { watchStore } from "../client/pi/chord/src/react/stores.ts";

const Notes = local("test.notes", { version: 1, params: {}, members: { text: str(), write: method(str()) } });

describe("a watch store", () => {
	it("reads the value and calls back on changes until unsubscribed", async () => {
		const host = createHost({ onError: () => {} });
		await host.load(
			[
				{
					name: "@test/notes",
					facet: defineFacet((h) =>
						h.serve(Notes, (_params, self) => {
							const text = self.writable("a");
							return {
								text: self.derive([text], ([value]) => value),
								write: async (next) => {
									text.set(next);
									return null;
								},
							};
						}),
					),
				},
			],
			context,
		);
		const notes = await host.get(Notes, {}, context);
		const store = watchStore(notes.text);
		assert.equal(store.snapshot(), "a");
		let calls = 0;
		const unsubscribe = store.subscribe(() => calls++);
		await pause(10);
		await notes.write("b", context);
		for (let index = 0; index < 100 && store.snapshot() !== "b"; index++) await pause(5);
		assert.equal(store.snapshot(), "b");
		const seen = calls;
		assert.ok(seen > 0);
		unsubscribe();
		await notes.write("c", context);
		await pause(20);
		assert.equal(calls, seen);
		notes.release();
		await host.close(context);
	});

	it("calls back once a held remote reference binds after the snapshot was read", async () => {
		const Server = hostKind("server");
		const Greeting = remote("test.greeting", {
			version: 1,
			params: {},
			hostAddress: () => Server(),
			variants: { main: { state: { text: str() } } },
		});
		const server = createHost({ address: Server(), principal: { kind: "server" }, onError: () => {} });
		await server.load(
			[
				{
					name: "@test/greeting",
					facet: defineFacet((h) =>
						h.serve(Greeting, () => ({ variant: "main", members: { text: constant("hi") } })),
					),
				},
			],
			context,
		);
		const client = createHost({ onError: () => {} });
		await client.load([], context);
		const held = client.hold(Greeting, {}, context);
		const store = watchStore(held.text);
		assert.equal(store.snapshot(), undefined);
		let calls = 0;
		const unsubscribe = store.subscribe(() => calls++);
		const [mine, theirs] = memoryLinkPair();
		server.accept(theirs, { principal: { kind: "user" } }, context);
		client.connect(mine, { principal: { kind: "server" } }, context);
		for (let index = 0; index < 200 && calls === 0; index++) await pause(5);
		assert.ok(calls > 0);
		assert.equal(store.snapshot(), "hi");
		unsubscribe();
		held.release();
		await client.close(context);
		await server.close(context);
	});

	it("gives undefined for no watchable and for one without a value", () => {
		assert.equal(watchStore(undefined).snapshot(), undefined);
		assert.equal(typeof watchStore(undefined).subscribe(() => {}), "function");
		const failing = {
			get value() {
				throw new Error("not ready");
			},
			watch: () => Promise.reject(new Error("x")),
		};
		assert.equal(watchStore(failing).snapshot(), undefined);
	});
});
