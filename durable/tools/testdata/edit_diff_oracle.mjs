// Run Pi's packages/durable/src/tools/edit-diff.ts (imported as-is with types stripped; the `diff` package import is stubbed because these functions never call it) over ops from stdin.
// Op: {op: "lineEnding" | "toLF" | "bom" | "fuzzyNormalize", text} | {op: "restore", text, ending} | {op: "fuzzyFind", content, oldText} | {op: "apply", content, edits: [{oldText, newText}], path}
// stdout: per op {ok} or {err} (an Error's message).
// usage: node edit_diff_oracle.mjs <packages dir>
import module from "node:module";
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "diff") return { url: "data:text/javascript,export {}", shortCircuit: true };
		return next(specifier, context);
	},
});
const edit = await import(pathToFileURL(`${packages}durable/src/tools/edit-diff.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		switch (op.op) {
			case "lineEnding": out.push({ ok: edit.detectLineEnding(op.text) }); break;
			case "toLF": out.push({ ok: edit.normalizeToLF(op.text) }); break;
			case "bom": out.push({ ok: edit.stripBom(op.text) }); break;
			case "fuzzyNormalize": out.push({ ok: edit.normalizeForFuzzyMatch(op.text) }); break;
			case "restore": out.push({ ok: edit.restoreLineEndings(op.text, op.ending) }); break;
			case "fuzzyFind": out.push({ ok: edit.fuzzyFindText(op.content, op.oldText) }); break;
			default: out.push({ ok: edit.applyEditsToNormalizedContent(op.content, op.edits, op.path) });
		}
	} catch (error) {
		out.push({ err: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
