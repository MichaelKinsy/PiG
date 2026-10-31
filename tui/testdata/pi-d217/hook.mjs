// Node module-customization hook (D217): the Pi oracles load the pinned Pi's modules, which lack the carried Pi fixes. This hook swaps
// the modules listed in manifest.json for the generated ones of this directory (gen.sh). A module is swapped only when the one it
// replaces has the SHA-256 the manifest records for the pinned build, so a different build fails loudly instead of being patched.
// Every other module loads unchanged.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

const manifest = JSON.parse(readFileSync(new URL("./manifest.json", import.meta.url), "utf8")).map((entry) => ({
	...entry,
	source: readFileSync(new URL(`./${entry.file}`, import.meta.url)),
}));

export async function load(url, context, nextLoad) {
	const result = await nextLoad(url, context);
	const entry = manifest.find((candidate) => url.endsWith(candidate.suffix));
	if (!entry) return result;
	const sha = createHash("sha256").update(Buffer.from(result.source)).digest("hex");
	if (sha !== entry.pinnedSha256) {
		throw new Error(`pi-d217 hook: ${url} is ${sha}, not the pinned ${entry.pinnedSha256}; regenerate tui/testdata/pi-d217 (gen.sh) or delete it if the pin passed c2760d32`);
	}
	return { ...result, source: entry.source, shortCircuit: true };
}
