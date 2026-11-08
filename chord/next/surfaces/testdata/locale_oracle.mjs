// Reads a JSON array of language tags on stdin; writes, for each, its canonical form (null when Intl refuses it) and its direction as Pi's
// directionOf decides it.
import { readFileSync } from "node:fs";

const RTL = new Set(["Adlm", "Arab", "Hebr", "Mand", "Nkoo", "Rohg", "Samr", "Syrc", "Thaa", "Yezi"]);
const tags = JSON.parse(readFileSync(0, "utf8"));
const out = tags.map((tag) => {
	let canon = null;
	try {
		canon = Intl.getCanonicalLocales(tag)[0] ?? null;
	} catch {}
	let dir = null;
	try {
		const script = new Intl.Locale(tag).maximize().script;
		dir = script !== undefined && RTL.has(script) ? "rtl" : "ltr";
	} catch {}
	return { canon, dir };
});
process.stdout.write(JSON.stringify(out));
