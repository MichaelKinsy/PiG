// Regenerates models-argument-errors.pi-1.0.0.json with Pi's executeCodemode over the catalog of
// TestModelsGlobalArgumentErrorsMatchPi. Run from the repository root after `npm ci` in extensions/sdk-ts:
//
//   node coding/extension/builtin/codemode/testdata/models-argument-errors.pi.mjs > coding/extension/builtin/codemode/testdata/models-argument-errors.pi-1.0.0.json
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const dist = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/extensions/codemode");
const { executeCodemode } = await import(`${dist}/execute.js`);
const { CODEMODE_DOCS_PATH } = await import(`${dist}/tool.js`);
const models = [
	{ type: "classifier", id: "judge", name: "Judge", api: "test-classifier", provider: "scorer", baseUrl: "https://c", input: ["text"], contextWindow: 1000 },
	{ type: "image", id: "painter", name: "Painter", api: "test-images", provider: "scorer", baseUrl: "https://i", input: ["text", "image"], output: ["text", "image"] },
	{ id: "chatty", name: "Chatty", api: "openai-completions", provider: "scorer", baseUrl: "https://x", input: ["text"] },
];
const typeOf = (model) => model.type ?? "chat";
const ofType = (type, provider) => models.filter((model) => typeOf(model) === type && (!provider || model.provider === provider));
const ctx = {
	tools: [{ name: "bash", description: "b", parameters: { type: "object" } }],
	modelRegistry: {
		getModelsOfType: ofType,
		getAvailableOfType: async (type, provider) => ofType(type, provider),
		getModelOfType: (type, provider, id) => ofType(type, provider).find((model) => model.id === id),
		classify: async () => {
			throw new Error("a malformed call reached the classifier");
		},
		generateImages: async () => {
			throw new Error("a malformed call reached the image model");
		},
	},
	sessionManager: { getBranch: () => [] },
	executeTool: async () => {
		throw new Error("no tool calls");
	},
};
const code = readFileSync(new URL("models-argument-errors.js", import.meta.url), "utf8");
const result = await executeCodemode("call", { code }, undefined, undefined, ctx, { models: true });
const text = result.content[1].text.split(CODEMODE_DOCS_PATH).join("<DOCS>");
process.stdout.write(`${JSON.stringify(JSON.parse(text), null, 1)}\n`);
