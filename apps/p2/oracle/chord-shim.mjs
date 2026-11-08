// `@earendil-works/chord` for the oracle: the Chord copy PiG vendors (Pi's v2 at the pin) plus what Pi main's root adds
// and p2 imports. The vendored copy is replaced by Pi main's chord with the Chord T1/T10/T11 lanes; this file shrinks as
// it lands. None of these additions takes part in a contract's fingerprint.
export * from "../../../chord/nextnode/plugins/node/pi/chord/src/next/index.ts";
import { ChordError } from "../../../chord/nextnode/plugins/node/pi/chord/src/next/index.ts";

/** Pi main's `invalid` and `forbidden` (packages/chord/src/data/errors.ts): errors a plugin throws for what it refuses. */
export function invalid(reason, message, details) {
	return new ChordError({ code: "invalid", reason, message, details });
}
export function forbidden(message, details) {
	return new ChordError({ code: "forbidden", message, details });
}
