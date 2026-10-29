// Derives the small committed fixture set from a full recording (`equivalence.sh record`): every upstream response keeps the first
// few models of each provider and every model whose id contains a string the catalog definitions mention, so each branch of the
// provider definitions still sees its input. The pruned fixtures are what the catalog tests replay; the golden output next to them
// is what Pi's generator writes for the same bytes (equivalence.sh compare).
//
//   node prune.mjs <full-fixtures-dir> <catalog-dir (packages/ai/catalog)> <out-dir>
import { mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [full, catalogDir, out] = process.argv.slice(2);

function sources(dir) {
	const found = [];
	for (const entry of readdirSync(dir)) {
		const path = join(dir, entry);
		if (statSync(path).isDirectory()) found.push(...sources(path));
		else if (path.endsWith(".ts")) found.push(readFileSync(path, "utf8"));
	}
	return found;
}

// Quoted strings that look like model ids or id fragments.
const tokens = new Set();
for (const text of sources(catalogDir)) {
	for (const match of text.matchAll(/["'`]([A-Za-z0-9._~/@-]{4,})["'`]/g)) {
		if (!match[1].includes("://") && !match[1].startsWith("../") && !match[1].startsWith("./")) tokens.add(match[1]);
	}
}
// A long token selects every id containing it; a short one only the ids it names (or names after a provider prefix or an alias mark).
const wanted = (id) => [...tokens].some((token) => (token.length >= 10 ? id.includes(token) : id === token || id.endsWith(`/${token}`) || id === `~${token}`));

const KEEP_FIRST = 3;
const index = JSON.parse(readFileSync(join(full, "index.json"), "utf8"));
mkdirSync(out, { recursive: true });
const rewritten = [];
for (const entry of index) {
	const body = JSON.parse(readFileSync(join(full, entry.file), "utf8"));
	let pruned = body;
	if (entry.url === "https://models.dev/experimental/v2.0/api.json") {
		pruned = {};
		for (const [provider, value] of Object.entries(body)) {
			const models = Object.entries(value.models ?? {});
			const keep = models.filter(([id], position) => position < KEEP_FIRST || wanted(id));
			pruned[provider] = { ...value, models: Object.fromEntries(keep) };
		}
	} else if (entry.url.startsWith("https://openrouter.ai/api/v1/models") && !entry.url.includes("decisions")) {
		const list = body.data ?? [];
		const keep = list.filter((model, position) => position < (entry.url.includes("image") ? 6 : 10) || wanted(model.id) || (model.reasoning?.supported_efforts?.length && position % 9 === 0) || model.pricing?.overrides?.length);
		pruned = { ...body, data: keep };
	} else if (entry.url === "https://ai-gateway.vercel.sh/v1/models") {
		const list = body.data ?? [];
		pruned = { ...body, data: list.filter((model, position) => position < 12 || wanted(model.id) || model.type === "evaluation" || (model.pricing?.input_tiers?.length)) };
	}
	writeFileSync(join(out, entry.file), JSON.stringify(pruned));
	rewritten.push(entry);
}
writeFileSync(join(out, "index.json"), `${JSON.stringify(rewritten, null, 2)}\n`);
