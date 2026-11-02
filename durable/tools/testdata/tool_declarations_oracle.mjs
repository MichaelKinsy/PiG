// Run Pi's built-in durable tools (packages/durable/src/tools, imported as-is with types stripped) and print what each declares to the model.
// stdout: one JSON object keyed by tool name: {description, parameters, structuredOutputSchema}, where parameters is JSON.stringify(tool.parameters) (the text, member order included) and structuredOutputSchema is the schema as an object, or null.
// usage: node tool_declarations_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const tools = await import(pathToFileURL(`${packages}durable/src/tools/index.ts`).href);
const out = {};
for (const tool of [tools.createReadTool(), tools.createWriteTool(), tools.createEditTool(), tools.createBashTool(), tools.createPowerShellTool()]) {
	out[tool.name] = {
		description: tool.description,
		parameters: JSON.stringify(tool.parameters),
		structuredOutputSchema: tool.structuredOutputSchema === undefined ? null : JSON.parse(JSON.stringify(tool.structuredOutputSchema)),
	};
}
process.stdout.write(JSON.stringify(out));
