// Run Pi's resolveAgent (packages/durable/src/harness/agent.ts, imported as-is with types stripped) over cases from stdin.
// Case: {extensions: [{name, tools: [{name, description}], sections: [{key}], wraps: [{kind: "tool" | "section", target, mode: "tag" | "rename" | "throw"}]}], settings?: [names], state?: AgentState}
// stdout: per case {agent: {model?, thinkingLevel, extensions, tools: [{name, description}], sections: [{key, text}], instructions?, cwd?}, reports: [message]}
// usage: node agent_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai") || specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const { resolveAgent } = await import(pathToFileURL(`${packages}durable/src/harness/agent.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const wrapper = (mode, tag, rename) => (item) => {
	if (mode === "throw") throw new Error("boom");
	if (mode === "rename") return rename(item);
	return tag(item);
};
const results = [];
for (const spec of JSON.parse(input)) {
	let counter = 0;
	const extensions = spec.extensions.map((extension) => ({
		name: extension.name,
		tools: extension.tools.map((tool) => ({ ...tool })),
		sections: extension.sections.map((section) => ({ key: section.key, render: () => `R:${section.key}:${extension.name}` })),
		wraps: extension.wraps.map((wrap) =>
			wrap.kind === "tool"
				? { tool: wrap.target, wrap: wrapper(wrap.mode, (tool) => ({ ...tool, description: `${tool.description}+${++counter}` }), (tool) => ({ ...tool, name: `${tool.name}x` })) }
				: { section: wrap.target, wrap: wrapper(wrap.mode, (section) => ({ ...section, render: () => `${section.render()}+${++counter}` }), (section) => ({ ...section, key: `${section.key}x` })) },
		),
	}));
	const snapshot = { installed: () => extensions, extension: (name) => extensions.find((extension) => extension.name === name) };
	const settings = spec.settings === undefined ? {} : { extensions: spec.settings.map((name) => ({ name })) };
	const reports = [];
	const agent = resolveAgent(spec.state, snapshot, settings, (error) => reports.push(error.message));
	results.push({
		agent: {
			...(agent.model === undefined ? {} : { model: agent.model }),
			thinkingLevel: agent.thinkingLevel,
			extensions: agent.extensions.map((extension) => extension.name),
			tools: agent.tools.map((tool) => ({ name: tool.name, description: tool.description })),
			sections: agent.sections.map((section) => ({ key: section.key, text: section.render({}) })),
			...(agent.instructions === undefined ? {} : { instructions: agent.instructions }),
			...(agent.cwd === undefined ? {} : { cwd: agent.cwd }),
		},
		reports,
	});
}
process.stdout.write(JSON.stringify(results));
