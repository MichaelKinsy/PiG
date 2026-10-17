// Runs Pi's message pipeline for the differential test: coding-agent convertToLlm, then the API's stream, which runs transformMessages and the per-API converter and POSTs the request to the baseUrl of the model. The Go test records that request.
// Protocol: one JSON request per stdin line {id, provider, modelId, baseUrl, systemPrompt, messages}; one JSON reply per line {id, error?}.
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
const ai = await import(pathToFileURL(`${root}/node_modules/@earendil-works/pi-ai/dist/compat.js`).href);
const core = await import(pathToFileURL(`${root}/dist/core/messages.js`).href);

process.env.AWS_ACCESS_KEY_ID = "AKIDEXAMPLE";
process.env.AWS_SECRET_ACCESS_KEY = "secret";
process.env.AWS_REGION = "us-east-1";

for await (const line of createInterface({ input: process.stdin })) {
	const request = JSON.parse(line);
	let reply;
	try {
		const base = ai.getModel(request.provider, request.modelId);
		if (!base) throw new Error(`unknown model ${request.provider}/${request.modelId}`);
		const model = { ...base, baseUrl: request.baseUrl };
		const messages = core.convertToLlm(request.messages);
		const context = { messages };
		if (request.systemPrompt) context.systemPrompt = request.systemPrompt;
		const stream = ai.stream(model, context, { apiKey: "test-key", maxRetries: 0 });
		const result = await stream.result();
		reply = { id: request.id, stopReason: result.stopReason, errorMessage: result.errorMessage };
	} catch (error) {
		reply = { id: request.id, error: String(error?.stack ?? error) };
	}
	process.stdout.write(`${JSON.stringify(reply)}\n`);
}
