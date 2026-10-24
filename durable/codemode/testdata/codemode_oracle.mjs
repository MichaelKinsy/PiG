// Run Pi's packages/durable/src/codemode/codemode.ts (imported as-is with types stripped): the code mode tool and
// extension declarations, the module's constants, its three documents, and its `codemode` prompt section.
// Op: {op: "static"} | {op: "variant", options: {models?, inlineBudget?, namespaces?}}
//   | {op: "section", options, callable: [{name, description, parameters, deferred?}]}
// stdout: per op its description of what Pi built.
// usage: node codemode_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-durable)
import { realpathSync } from "node:fs";
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
// The mirror is reached through a symlink; modules report their real path.
const sourceRoot = pathToFileURL(realpathSync(packages)).href;
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		if (context.parentURL?.startsWith(sourceRoot) && !/^(\.|\/|node:|file:|data:)/.test(specifier)) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
});
const codemode = await import(pathToFileURL(`${packages}durable/src/codemode/codemode.ts`).href);

const describeDoc = (token) => {
	const definition = token.definition;
	return {
		kind: definition.kind,
		version: definition.version,
		scope: definition.scope,
		history: definition.history ?? null,
		fork: definition.fork ?? null,
		initial: definition.initial(),
		checkpoints: Array.from({ length: 140 }, (_, deltasSinceBase) => definition.checkpointWhen({}, [], { deltasSinceBase })),
	};
};
const options = (raw) => ({
	sandbox: {},
	...(raw.models === null || raw.models === undefined ? {} : { models: raw.models }),
	...(raw.inlineBudget === null || raw.inlineBudget === undefined ? {} : { inlineBudget: raw.inlineBudget }),
	namespaces: raw.namespaces,
});

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		switch (op.op) {
			case "static":
				out.push({
					ok: {
						moduleVersion: codemode.CODEMODE_MODULE_VERSION,
						toolName: codemode.CODEMODE_TOOL_NAME,
						run: describeDoc(codemode.CodemodeRunDoc),
						journal: describeDoc(codemode.CodemodeJournalDoc),
						store: describeDoc(codemode.CodemodeStoreDoc),
					},
				});
				break;
			case "variant": {
				const tool = codemode.createCodemodeTool(options(op.options));
				const extension = codemode.createCodemodeExtension(options(op.options));
				out.push({
					ok: {
						tool: {
							name: tool.name,
							description: tool.description,
							parameters: tool.parameters,
							outputLimits: tool.outputLimits,
							constrainedSampling: tool.constrainedSampling,
							callers: tool.callers,
							replay: tool.replay,
						},
						extension: { name: extension.name, tools: extension.tools.map((each) => each.name), sections: extension.sections.map((each) => each.key) },
					},
				});
				break;
			}
			case "section": {
				const extension = codemode.createCodemodeExtension(options(op.options));
				const rendered = await extension.sections[0].render({ agent: { callable: op.callable } });
				out.push({ ok: rendered ?? null });
				break;
			}
			default:
				throw new Error(`unknown op ${op.op}`);
		}
	} catch (error) {
		out.push({ err: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
