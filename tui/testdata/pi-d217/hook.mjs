// Node module-customization hook (D217): the Pi oracle scripts load the built pinned pi-tui, whose utils.js lacks the carried Pi fixes.
// This hook swaps that one module for utils.js of this directory (the pin's utils.ts with the carried hunks applied, compiled as the pin
// build compiles it; see gen.sh). It refuses to swap unless the module it replaces is the pinned build's, byte for byte. Every other
// module loads unchanged.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

const PINNED_UTILS_SHA256 = readFileSync(new URL("./pinned-utils.sha256", import.meta.url), "utf8").trim();
const patched = readFileSync(new URL("./utils.js", import.meta.url));
const TARGET = "/@earendil-works/pi-tui/dist/utils.js";

export async function load(url, context, nextLoad) {
	const result = await nextLoad(url, context);
	if (!url.endsWith(TARGET)) return result;
	const original = Buffer.from(result.source);
	const sha = createHash("sha256").update(original).digest("hex");
	if (sha !== PINNED_UTILS_SHA256) {
		throw new Error(`pi-d217 hook: ${url} is ${sha}, not the pinned build ${PINNED_UTILS_SHA256}; regenerate tui/testdata/pi-d217 (gen.sh) or delete it if the pin passed c2760d32`);
	}
	return { ...result, source: patched, shortCircuit: true };
}
