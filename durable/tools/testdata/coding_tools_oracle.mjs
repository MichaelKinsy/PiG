// Run Pi's coding tools (packages/durable/src/tools/{index,read}.ts, imported as-is with types stripped) over cases from stdin.
// Case: {op: "tools", images: bool} answers the coding-tools extension: its name and each tool's name, description and parameters.
//       {op: "read", images: "none" | "stub", prepared?: PreparedImage | null, files: {name: base64}, path, model?: {input, resize?}} runs read on a temp directory with a stub image processor answering `prepared`
//       (null: cannot be prepared) and answers {output, isError, diagnostics, error?, seen?: {mimeType, size, limits}} (seen: what the processor was given).
// usage: node coding_tools_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import module from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai") || specifier === "typebox" || specifier.startsWith("typebox/") || specifier === "diff")
			return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const { createCodingTools } = await load("durable/src/tools/index.ts");
const { createReadTool } = await load("durable/src/tools/read.ts");
const { NodeExecutionEnv } = await load("durable/src/env/node.ts");
const { BACKGROUND_CONTEXT } = await load("chord/src/context/index.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const c of JSON.parse(input)) {
	if (c.op === "tools") {
		const extension = createCodingTools(c.images ? { images: { prepare: async () => undefined } } : {});
		out.push({ name: extension.name, tools: extension.tools.map((t) => ({ name: t.name, description: t.description, parameters: JSON.parse(JSON.stringify(t.parameters)) })) });
		continue;
	}
	if (c.op === "run") {
		// {op: "run", tool, args, files: {name: base64}}: runs one tool of createCodingTools() in a temp directory and answers its result, the
		// output the tool streamed and every file left behind (base64), or {error, files} when it throws.
		const directory = mkdtempSync(join(tmpdir(), "pig-oracle-run-"));
		try {
			for (const [name, data] of Object.entries(c.files)) {
				mkdirSync(dirname(join(directory, name)), { recursive: true });
				writeFileSync(join(directory, name), Buffer.from(data, "base64"));
			}
			const streamed = [];
			const diagnostics = [];
			const api = { env: new NodeExecutionEnv({ cwd: directory }), output: (text) => { streamed.push(text); }, diagnostic: (d) => { diagnostics.push(d); }, details: async () => {}, retainedOutput: () => ({ text: streamed.join(""), truncated: false }), outputWindow: undefined };
			const files = () => Object.fromEntries(readdirSync(directory, { recursive: true, withFileTypes: true }).filter((e) => e.isFile()).map((e) => { const path = join(e.parentPath, e.name); return [path.slice(directory.length + 1), readFileSync(path).toString("base64")]; }));
			const tool = createCodingTools({}).tools.find((candidate) => candidate.name === c.tool);
			const scrub = (value) => JSON.parse(JSON.stringify(value).replaceAll(directory, "<dir>"));
			try {
				const result = await tool.execute(c.args, api, BACKGROUND_CONTEXT);
				out.push(scrub({ output: result.output ?? null, structuredOutput: result.structuredOutput ?? null, details: result.details ?? null, isError: result.isError ?? false, diagnostics: [...diagnostics, ...(result.diagnostics ?? [])], streamed: streamed.join(""), files: files() }));
			} catch (error) {
				out.push(scrub({ error: error.message, files: files() }));
			}
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
		continue;
	}
	const directory = mkdtempSync(join(tmpdir(), "pig-oracle-read-"));
	try {
		for (const [name, data] of Object.entries(c.files)) writeFileSync(join(directory, name), Buffer.from(data, "base64"));
		const seen = {};
		const options = c.images === "stub" ? { images: { prepare: async (bytes, mimeType, limits) => { seen.mimeType = mimeType; seen.size = bytes.length; seen.limits = limits; return c.prepared === null ? undefined : c.prepared; } } } : {};
		const model = { input: c.model?.input ?? ["text", "image"], ...(c.model?.resize === undefined ? {} : { inputLimits: { images: { resize: c.model.resize } } }) };
		const api = { env: new NodeExecutionEnv({ cwd: directory }), output: () => {}, diagnostic: () => {}, details: async () => {}, agent: async () => ({ model: { provider: "test", modelId: "test" } }), models: { getModel: () => model } };
		try {
			const result = await createReadTool(options).execute({ path: c.path }, api, BACKGROUND_CONTEXT);
			out.push({ output: result.output, isError: result.isError ?? false, diagnostics: result.diagnostics ?? [], seen });
		} catch (error) {
			out.push({ error: error.message });
		}
	} finally {
		rmSync(directory, { recursive: true, force: true });
	}
}
process.stdout.write(JSON.stringify(out));
