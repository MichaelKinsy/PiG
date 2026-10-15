// Run Pi's McpClient.listTools (packages/mcp/src/client.ts, types stripped) against scripted tools/list pages.
// Case: {pages: [<result object or {"$error": message}>, ...]} - the n-th tools/list request gets pages[n] (the last page repeats).
// Answers per case {tools: [...], requests: [<params of each tools/list request>]} or {error: message}.
// usage: node list_tools_oracle.mjs <packages dir>   cases as JSON on stdin
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const load = (path) => import(pathToFileURL(`${packages}mcp/src/${path}`).href);
const { McpClient } = await load("client.ts");
const { LATEST_PROTOCOL_VERSION } = await load("protocol/types.ts");
const { createInMemoryTransportPair } = await load("testing/index.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const c of JSON.parse(input)) {
	const pair = createInMemoryTransportPair();
	const requests = [];
	let n = 0;
	pair.server.onMessage((message) => {
		if (!("id" in message) || !("method" in message)) return;
		queueMicrotask(async () => {
			let result;
			if (message.method === "initialize") {
				result = { protocolVersion: LATEST_PROTOCOL_VERSION, capabilities: { tools: {} }, serverInfo: { name: "s", version: "1" } };
			} else {
				requests.push(message.params ?? null);
				result = c.pages[Math.min(n++, c.pages.length - 1)];
			}
			await pair.server.send({ jsonrpc: "2.0", id: message.id, result });
		});
	});
	await pair.server.start();
	const client = new McpClient({ name: "c", version: "1" });
	await client.connect(pair.client);
	try {
		out.push({ tools: await client.listTools(), requests });
	} catch (error) {
		out.push({ error: error.message });
	}
	await client.close();
}
process.stdout.write(JSON.stringify(out));
