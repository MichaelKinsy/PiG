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
// frames arrive in order, a failure of the Go object fails this object `transient`) and its methods call the Go object. A
// keyed member is a document handle of its object and cannot be forwarded: the Go side refuses such a contract before it
// ships it.
import { defineFacet } from "@earendil-works/chord/next";
import { contractOf } from "./contracts.mjs";
import { decodeValue, encodeValue } from "./wire.mjs";

/**
 * The proxy facets of one driver: `rpc` is the Go side's request channel (`ask`, `tell`, and `watches`: the observers of the
 * Go host's watches by the id this side chose).
 */
export function createProxies({ ask, tell, watches }) {
	let nextWatch = 0;

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

	/** What a factory of `data`'s type returns: its members, forwarded to the Go host's object for `params`. */
	function implementation(facet, data, params, self) {
		const held = self.open(
			async (context) => (await ask("get", { facet, contract: data.name, params }, context)).handle,
			(handle) => tell("release", { handle }),
		);
		const members = {};
		for (const [name, node] of Object.entries(data.normal.members)) {
			if (Object.hasOwn(node, "state")) {
				members[name] = self.follow((observer, context) => follow(held.value, name, observer, context));
			} else {
				const noArgs = node.args.type === "null" && Object.keys(node.args).length === 1;
				const call = (args, context) => ask("bridgeCall", { handle: held.value, method: name, args: encodeValue(args) }, context).then(({ result }) => decodeValue(result));
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
				host.serve(contract, (params, self) => implementation(spec.name, data, params, self));
			}
		});
	}

	return { proxyFacet };
}
