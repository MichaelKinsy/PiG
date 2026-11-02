// Contracts as data (docs/plan/chord-plugins-node-evaluator.md 10.2): the Go side sends a contract as the normal form its
// fingerprint hashes (kind, params, members or variants, each with its schemas' normal forms) with its name, version and
// fingerprint, and `contractOf` declares the Pi contract of it with Pi's own `local`/`remote`. The rebuilt contract hashes to
// the fingerprint it crossed with, or `contractOf` throws: a type that differs from what the other side declared is refused
// as Pi refuses a contract of another fingerprint.
import { keyed, local, method, remote } from "@earendil-works/chord/next";

/**
 * A local contract's members as its declaration: `{ state }` is a state schema, `{ keyed }` a keyed member and
 * `{ args, result }` a method.
 */
function localMembers(members) {
	return Object.fromEntries(
		Object.entries(members).map(([name, node]) => {
			if (Object.hasOwn(node, "state")) return [name, node.state];
			if (Object.hasOwn(node, "keyed")) return [name, keyed(node.keyed)];
			return [name, method(node.args, node.result)];
		}),
	);
}

/** A remote variant's declaration: its state schemas and its methods. */
function remoteVariant(members) {
	const state = {};
	const methods = {};
	for (const [name, node] of Object.entries(members)) {
		if (Object.hasOwn(node, "state")) state[name] = node.state;
		else methods[name] = method(node.args, node.result);
	}
	return { state, methods };
}

/**
 * The Pi contract of `data` (`{ name, version, fingerprint, normal }`); it throws when the rebuilt contract does not hash to
 * the fingerprint it crossed with, unless `options.verify` is `false`. A remote contract's `hostAddress` is not part of the
 * fingerprint and is a function of the app that declared it: `options.hostAddress` is it, and a remote contract rebuilt
 * without one cannot be held (its address function throws).
 */
export function contractOf(data, options = {}) {
	const { name, version, fingerprint, normal } = data;
	const params = Object.fromEntries(normal.params);
	const contract =
		normal.kind === "local"
			? local(name, { version, params, members: localMembers(normal.members) })
			: remote(name, {
					version,
					params,
					hostAddress:
						options.hostAddress ??
						(() => {
							throw new Error(`${name}: a remote contract that crossed as data has no host address`);
						}),
					variants: Object.fromEntries(
						Object.entries(normal.variants).map(([variant, members]) => [variant, remoteVariant(members)]),
					),
				});
	if (options.verify !== false && contract.fingerprint !== fingerprint) {
		throw new Error(`${name}: crossed as data with fingerprint ${fingerprint}, rebuilt with ${contract.fingerprint}`);
	}
	return contract;
}

/**
 * `describe` (control): the contract the Node host rebuilds from `request.contract`, as it hashes (unverified): its kind,
 * name, version and fingerprint.
 */
async function describe(request) {
	const { kind, name, version, fingerprint } = contractOf(request.contract, { verify: false });
	return { kind, name, version, fingerprint };
}

export const operations = { describe };
