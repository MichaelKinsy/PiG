// The Go host's own facets, as facets of this process's host (docs/plan/chord-plugins-node-evaluator.md 10.4): for a Go core
// facet that serves local types, a proxy facet serves the same contracts here (rebuilt from the data the Go side shipped) with
// a factory that forwards to the Go host's own object of that type over the control channel. The proxy is an ordinary facet
// in this host's load and reload sets (the Go side ships it as a script of the plugins' runtime, answered with the host
// package below), so Pi's closure rule and the runtime's `adopt` rule apply to it as to any plugin's facet: a plugin that
// requests a type of the Go core facet binds to the proxy, and a Go core facet that failed on the Go host is shipped as a
// failed facet, so its dependents keep their old code.
//
// An object of a proxied type is held on the Go host by one reference, opened as a native of this object's attempt (`get`)
// and released when the attempt ends (`release`), its state members follow the Go host's watches (`self.follow`: the
// frames arrive in order, a failure of the Go object fails this object `transient`) and its methods call the Go object.
//
// A keyed member is a keyed document handle of this object (`self.state` of a keyed definition made from the member's schema,
// Pi's own handle), whose documents are a copy of the Go object's: the object goes live with every document the Go member
// holds (`mirror`: a snapshot, read after the Go side registered its observer), each later commit of the Go member reaches the copy
// as `mirrorDoc` frames in the Go host's commit order, and a Go object that ended (its facet reloaded) is read again as one
// `mirrorReset`. A method's answer follows the frames of the commits it made on the one channel and is returned after the copy
// applied them, so a read of the member after an awaited call sees what the call wrote. The copy is read-only for plugins as a
// reference's keyed member is (`get`, `watch`, `at`; a plugin has no `change` of another object's keyed member).
import { defineFacet, defineState } from "@earendil-works/chord";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { contractOf } from "./contracts.mjs";
import { decodeValue, encodeValue } from "./wire.mjs";

/**
 * A document that a schema accepts, for the `initial()` of a keyed definition made from it: what a key reads as while absent, and
 * what a draft of a new document starts from. It follows the schema's `default`, a constant, the first choice of a union,
 * and the least value its bounds allow; a schema it cannot make a value of is refused.
 */
function valueOf(schema) {
	if (Object.hasOwn(schema, "default")) return schema.default;
	if (Object.hasOwn(schema, "const")) return schema.const;
	if (Array.isArray(schema.enum)) return schema.enum[0];
	for (const choice of [schema.anyOf, schema.oneOf]) if (Array.isArray(choice) && choice.length > 0) return valueOf(choice[0]);
	const type = Array.isArray(schema.type) ? schema.type[0] : schema.type;
	switch (type) {
		case "object": {
			const out = {};
			const required = new Set(schema.required ?? []);
			for (const [name, member] of Object.entries(schema.properties ?? {})) if (required.has(name)) out[name] = valueOf(member);
			return out;
		}
		case "array":
			return Array.from({ length: schema.minItems ?? 0 }, () => valueOf(schema.items ?? {}));
		case "string":
			return "a".repeat(schema.minLength ?? 0);
		case "integer":
		case "number": {
			const low = schema.minimum ?? (schema.exclusiveMinimum === undefined ? undefined : schema.exclusiveMinimum + 1);
			return low === undefined || low < 0 ? Math.max(0, schema.maximum ?? 0) : low;
		}
		case "boolean":
			return false;
		case "null":
			return null;
		default:
			if (Object.keys(schema).length === 0) return null;
			throw new Error(`no value of the schema ${JSON.stringify(schema)}`);
	}
}

/** Makes `draft` the document `value` (a root replacement: its properties are removed, then set). */
function replaceRoot(draft, value) {
	for (const name of Object.keys(draft)) if (!Object.hasOwn(value, name)) delete draft[name];
	for (const [name, member] of Object.entries(value)) draft[name] = member;
}

/**
 * The copy of one keyed member: it applies what the Go side sends, in order, to the object's keyed handle `handle`; `idle()`
 * settles once everything received so far was applied.
 */
function createCopy(handle, fatal) {
	let chain = Promise.resolve();
	const present = new Set();
	const { promise: ready, resolve: markReady } = Promise.withResolvers();
	const enqueue = (work) => {
		// A document change of an object that ended (its attempt is sealed) is `released`: the copy is gone with the object, not a failure.
		chain = chain.then(work).catch((error) => {
			if (error?.code !== "released") fatal(error);
		});
		return chain;
	};
	const put = async (key, value) => {
		present.add(key);
		await handle.change(key, (draft) => replaceRoot(draft, value), BACKGROUND_CONTEXT);
	};
	const retire = async (key) => {
		present.delete(key);
		await handle.retire(key, BACKGROUND_CONTEXT);
	};
	return {
		put: (key, value) => enqueue(() => put(key, value)),
		retire: (key) => enqueue(() => retire(key)),
		/** Makes the copy `docs` (`[{ key, value }]`): the documents that are not in it are retired. The first one makes the copy `ready`. */
		reset: (docs) =>
			enqueue(async () => {
				const keys = new Set(docs.map((doc) => doc.key));
				for (const key of [...present]) if (!keys.has(key)) await retire(key);
				for (const doc of docs) await put(doc.key, doc.value);
				markReady();
			}),
		/** Settles once the first reset was applied (the Go side sends it before every other frame). */
		ready,
		idle: () => chain,
	};
}

/**
 * The proxy facets of one driver: `rpc` is the Go side's request channel (`ask`, `tell`, `watches`: the observers of the
 * Go host's watches by the id this side chose, and `mirrors`: the copies of its keyed members by theirs).
 */
export function createProxies({ ask, tell, watches, mirrors, fatal }) {
	let nextWatch = 0;
	let nextMirror = 0;

	/** The state member `member` of the Go object held as `handle`, followed: its frames reach `observer` in order. */
	async function follow(handle, member, observer, context) {
		const id = ++nextWatch;
		// Registered before the request is sent: the frames that follow its reply find the observer.
		watches.set(id, observer);
		let acquired;
		try {
			acquired = await ask("watch", { handle, member, watch: id }, context);
		} catch (error) {
			watches.delete(id);
			throw error;
		}
		return {
			value: decodeValue(acquired.value),
			detach() {
				if (watches.delete(id)) tell("unwatch", { watch: id });
			},
		};
	}

	/** The copy of keyed member `member` of the Go object held as `handle`, applied to `keyed`, with the documents it has now. */
	async function mirror(handle, member, keyed, context) {
		const id = ++nextMirror;
		const copy = createCopy(keyed, fatal);
		// Registered before the request is sent: the frames that follow its reply find the copy.
		mirrors.set(id, copy);
		try {
			await ask("mirror", { handle, member, mirror: id }, context);
			// The first frame the Go side sends is the reset with the member's documents; every other frame follows it.
			await copy.ready;
		} catch (error) {
			mirrors.delete(id);
			tell("unmirror", { mirror: id });
			throw error;
		}
		return {
			idle: () => copy.idle(),
			stop() {
				if (mirrors.delete(id)) tell("unmirror", { mirror: id });
			},
		};
	}

	/** What a factory of `data`'s type returns: its members, forwarded to the Go host's object for `params`. */
	function implementation(facet, data, params, self, keyedDefinitions) {
		const keyed = new Map(keyedDefinitions.map(([name, definition]) => [name, self.state(definition)]));
		const held = self.open(
			async (context) => {
				const { handle } = await ask("get", { facet, contract: data.name, params }, context);
				const copies = [];
				try {
					for (const [name, handles] of keyed) copies.push(await mirror(handle, name, handles, context));
				} catch (error) {
					for (const copy of copies) copy.stop();
					tell("release", { handle });
					throw error;
				}
				return { handle, copies };
			},
			({ handle, copies }) => {
				for (const copy of copies) copy.stop();
				tell("release", { handle });
			},
		);
		const members = {};
		for (const [name, node] of Object.entries(data.normal.members)) {
			if (Object.hasOwn(node, "state")) {
				members[name] = self.follow((observer, context) => follow(held.value.handle, name, observer, context));
			} else if (Object.hasOwn(node, "keyed")) {
				members[name] = keyed.get(name);
			} else {
				const noArgs = node.args.type === "null" && Object.keys(node.args).length === 1;
				const call = async (args, context) => {
					const { result } = await ask("bridgeCall", { handle: held.value.handle, method: name, args: encodeValue(args) }, context);
					// The commits of the call reached the copies before its answer: a read of a keyed member sees them.
					await Promise.all(held.value.copies.map((copy) => copy.idle()));
					return decodeValue(result);
				};
				members[name] = noArgs ? (context) => call(null, context) : call;
			}
		}
		return members;
	}

	/** The facet of the Go core facet `spec.name`: it serves the contracts of `spec.contracts` (ContractData). */
	function proxyFacet(spec) {
		return defineFacet((host) => {
			for (const data of spec.contracts) {
				const contract = contractOf(data);
				// One keyed definition per keyed member, named after it: the documents of an object's copy.
				const keyedDefinitions = Object.entries(data.normal.members)
					.filter(([, node]) => Object.hasOwn(node, "keyed"))
					.map(([name, node]) => [
						name,
						defineState(`@go/${data.name}/${name}`, { schema: node.keyed, version: 1, initial: () => valueOf(node.keyed), keyed: true }),
					]);
				host.serve(contract, (params, self) => implementation(spec.name, data, params, self, keyedDefinitions));
			}
		});
	}

	return { proxyFacet };
}
