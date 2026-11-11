// Prints hash64(canonicalJson(FrameSchema)) and the length of that text for Pi's wire.ts. FrameSchema is not exported,
// so the module is copied next to itself with an export added, imported, and the copy removed. Run from the repository
// root with Node 24 or later (add --conditions=source so pi-schema resolves to its sources):
//
//	node --conditions=source chord/host/testdata/pi_frame_schema_hash.mjs
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { canonicalJson, hash64 } from "../../../.upstream/current/packages/chord/src/schema.ts";

const wire = new URL("../../../.upstream/current/packages/chord/src/wire.ts", import.meta.url);
const copy = new URL("wire-frame-schema.ts", wire);
writeFileSync(copy, `${readFileSync(wire, "utf8")}\nexport { FrameSchema };\n`);
try {
	const { FrameSchema } = await import(copy.href);
	const text = canonicalJson(FrameSchema);
	console.log(hash64(text), text.length);
} finally {
	rmSync(copy);
}
