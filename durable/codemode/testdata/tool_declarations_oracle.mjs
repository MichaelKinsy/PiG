// Run Pi's code mode tool, tool_search tool and model tools (imported as-is with types stripped) and print what each declares to the model.
// stdout: one JSON object keyed by tool name: {description, parameters, structuredOutputSchema}, where parameters is JSON.stringify(tool.parameters) (the text, member order included) and structuredOutputSchema is the schema as an object, or null.
// usage: node tool_declarations_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const codemode = await import(pathToFileURL(`${packages}durable/src/codemode/codemode.ts`).href);
const models = await import(pathToFileURL(`${packages}durable/src/codemode/models.ts`).href);
const search = await import(pathToFileURL(`${packages}durable/src/tool-search/tool-search.ts`).href);
const out = {};
for (const tool of [codemode.createCodemodeTool({ sandbox: {} }), search.createToolSearchTool(), ...models.createModelTools()]) {
	out[tool.name] = {
		description: tool.description,
		parameters: JSON.stringify(tool.parameters),
		structuredOutputSchema: tool.structuredOutputSchema === undefined ? null : JSON.parse(JSON.stringify(tool.structuredOutputSchema)),
	};
}
process.stdout.write(JSON.stringify(out));
