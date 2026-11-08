// This process's host is the plugins' half of one logical host (D605): the Go server host serves the app's own remote types
// (the locale and theme choices, the telemetry ingest), this host serves the plugins', and both have the same address. Pi's one
// host serves the union, so a plugin's hold of a type the Go host serves binds there. Pi's `Destination` places every remote
// type whose host address is this host's here, served here or not, so a hold of a type the Go host serves would wait
// `<type> is not served here` for ever. The host imports this module in its place (resolve.mjs; Pi's file is not edited): it
// is Pi's `Destination` but for a remote type placed here that the Go host serves, which goes over the parent link, as a hold of
// a type placed elsewhere does (the Go host's delegation, the other direction). The hold then acts as its principal and meets
// the Go host's own object, whatever its variants and members.
import { contractData } from "./pi/chord/src/contracts/internal.ts";
import { Destination as PiDestination } from "./pi/chord/src/host/destination.ts";

/** Whether the Go host serves a type by name (`RuntimeLoad.ParentServes`); `undefined` without one. */
let parentServes;
/** The answers of `parentServes`, by name, until the next load or reload. */
const answers = new Map();

/** Sets the Go host's answer for the host this process loads (the synchronous callback), and forgets the earlier ones. */
export function setParentServes(fn) {
	parentServes = fn;
	answers.clear();
}

/** Forgets the answers: what the Go host serves can change with the code a reload applies. */
export function forgetParentServes() {
	answers.clear();
}

/** Whether `Type` is a remote type the Go host serves. */
function servedByParent(Type) {
	if (parentServes === undefined || Type.kind !== "remote") return false;
	let served = answers.get(Type.name);
	if (served === undefined) {
		served = parentServes(Type.name);
		answers.set(Type.name, served);
	}
	return served;
}

/** Pi's `Destination` with the Go host's types going over the parent link. */
export class Destination extends PiDestination {
	#parent;

	constructor(address, routes, parent) {
		super(address, routes, parent);
		this.#parent = parent;
	}

	reference(Type, params, principal, context) {
		const placed = super.reference(Type, params, principal, context);
		if (placed.kind !== "here" || !servedByParent(Type)) return placed;
		// What `Destination` computed for the placement (a `hostAddress` that threw has thrown already).
		const { kind, params: values } = contractData(Type).hostAddress(params);
		return { kind: "path", path: this.#parent, address: { kind, params: values } };
	}

	placedHere(Type, params) {
		return super.placedHere(Type, params) && !servedByParent(Type);
	}
}

