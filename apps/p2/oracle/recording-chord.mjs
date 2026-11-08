// Pi's Chord with its contract constructors recorded: a contract a module declares without exporting it (a facet's private
// `local(...)`) is still one p2's clients and peers bind by fingerprint. `fingerprints.mjs` points `@earendil-works/chord` here.
import * as chord from "ORACLE_CHORD_INDEX";

export * from "ORACLE_CHORD_INDEX";

export const declared = [];
const record = (make) => (...args) => {
	const contract = make(...args);
	declared.push(contract);
	return contract;
};
export const local = record(chord.local);
export const remote = record(chord.remote);
