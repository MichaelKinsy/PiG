// Runs Pi's provider modules for the tool schema differential: for each target and case it sends one request with the case's tools to a local server that refuses it, and prints
// {"<target>/<case>": "<request body>"} as one JSON object. Input on stdin: {"wire": <a tool's parameters JSON, parsed as Pi parses an extension's>}.
import http from "node:http";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
const input = JSON.parse(readFileSync(0, "utf8"));
const nm = `${root}/node_modules/@earendil-works`;
const ai = await import(pathToFileURL(`${nm}/pi-ai/dist/compat.js`).href);
const schema = await import(pathToFileURL(`${nm}/pi-schema/dist/index.js`).href);
const tools = await import(pathToFileURL(`${root}/dist/core/tools/index.js`).href);
process.env.AWS_ACCESS_KEY_ID = "AKIDEXAMPLE";
process.env.AWS_SECRET_ACCESS_KEY = "secret";
process.env.AWS_REGION = "us-east-1";
process.env.AWS_BEDROCK_FORCE_HTTP1 = "1";

const { obj, str, opt, num } = schema;
const enumTool = {
	name: "enum_probe",
	description: "probe",
	parameters: obj({ zeta: str({ description: "z" }), mode: ai.StringEnum(["fast", "slow"], { description: "how", default: "fast" }), alpha: opt(num()) }),
};
const all = tools.createAllTools(process.cwd());
const builtin = ["read", "bash", "edit", "write", "grep", "find", "ls"].map((name) => all[name]);
const wireTool = { name: "wire_probe", description: "probe", parameters: input.wire };
const cases = { enum: [enumTool], builtin, wire: [wireTool] };

const targets = {
	anthropic: ["anthropic", "claude-sonnet-4-5"],
	"openai-responses": ["openai", "gpt-5"],
	"azure-responses": ["azure", "gpt-4.1"],
	"completions-groq": ["groq", "llama-3.1-8b-instant"],
	google: ["google", "gemini-2.5-flash"],
	mistral: ["mistral", "devstral-medium-latest"],
	bedrock: ["amazon-bedrock", "anthropic.claude-sonnet-4-5-20250929-v1:0"],
};

const bodies = {};
for (const [target, [provider, id]] of Object.entries(targets)) {
	for (const [name, caseTools] of Object.entries(cases)) {
		let body;
		const server = http.createServer((req, res) => {
			const chunks = [];
			req.on("data", (chunk) => chunks.push(chunk));
			req.on("end", () => {
				body ??= Buffer.concat(chunks).toString();
				res.writeHead(400, { "content-type": "application/json" });
				res.end('{"error":{"message":"capture"}}');
			});
		});
		await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
		const base = ai.getModel(provider, id);
		if (!base) throw new Error(`unknown model ${provider}/${id}`);
		const model = { ...base, baseUrl: `http://127.0.0.1:${server.address().port}` };
		const stream = ai.stream(model, { messages: [{ role: "user", content: "hi", timestamp: 1 }], tools: caseTools }, { apiKey: "test-key", maxRetries: 0 });
		await stream.result();
		server.close();
		if (body === undefined) throw new Error(`${target}/${name} sent no request`);
		bodies[`${target}/${name}`] = body;
	}
}
process.stdout.write(JSON.stringify(bodies));
