// Probe the pinned openai SDK (7.59.0) AzureOpenAI client's request URL for Azure Responses, matching Pi's createClient (azure-openai-responses.ts:154-171) + resolveAzureConfig (azure-openai-config.ts:72-107).
// Input (stdin): [{baseURL, apiVersion, resolve, resourceName, envBaseURL, modelBaseURL}]
//   - If resolve is true, runs Pi's normalizeAzureBaseUrl / resolveAzureBaseUrl logic (as in azure-openai-config.ts) first, then passes the resolved baseUrl and apiVersion to new AzureOpenAI({ apiKey, apiVersion, dangerouslyAllowBrowser: true, fetch, baseURL }).
//   - Otherwise passes baseURL and apiVersion directly to new AzureOpenAI(...).
// Output per case: {resolvedBaseURL, apiVersion, url, creds, error}.
import { pathToFileURL } from "node:url";
import path from "node:path";

const sdk = path.join(import.meta.dirname, "sdk", "node_modules");
const { AzureOpenAI } = await import(pathToFileURL(path.join(sdk, "openai", "index.mjs")).href);

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const stream = () => new Response("data: [DONE]\n\n", { headers: { "content-type": "text/event-stream" } });

// Mirror of packages/ai/src/api/azure-openai-config.ts:37-107 at Pi v1.5.2.
function normalizeAzureBaseUrl(baseUrl) {
	const trimmed = baseUrl.trim().replace(/\/+$/, "");
	let url;
	try {
		url = new URL(trimmed);
	} catch {
		throw new Error(`Invalid Azure OpenAI base URL: ${baseUrl}`);
	}
	const isAzureHost =
		url.hostname.endsWith(".openai.azure.com") ||
		url.hostname.endsWith(".cognitiveservices.azure.com") ||
		url.hostname.endsWith(".ai.azure.com");
	const normalizedPath = url.pathname.replace(/\/+$/, "");
	if (
		isAzureHost &&
		(normalizedPath === "" ||
			normalizedPath === "/" ||
			normalizedPath === "/openai" ||
			normalizedPath === "/openai/v1/responses")
	) {
		url.pathname = "/openai/v1";
		url.search = "";
	}
	return url.toString().replace(/\/+$/, "");
}

function resolveAzureConfig(c) {
	let resolvedBaseUrl = c.envBaseURL?.trim() || undefined;
	if (!resolvedBaseUrl && c.resourceName) {
		resolvedBaseUrl = `https://${c.resourceName}.openai.azure.com/openai/v1`;
	}
	if (!resolvedBaseUrl && c.modelBaseURL) {
		resolvedBaseUrl = c.modelBaseURL;
	}
	if (!resolvedBaseUrl) {
		throw new Error("Azure OpenAI base URL is required.");
	}
	return {
		baseUrl: normalizeAzureBaseUrl(resolvedBaseUrl),
		apiVersion: c.apiVersion || "v1",
	};
}

const run = async (c) => {
	const out = {};
	const fetch = async (url) => {
		out.url = String(url);
		try { new Request(url); } catch { out.creds = true; }
		return stream();
	};
	try {
		let baseURL = c.baseURL;
		let apiVersion = c.apiVersion || "v1";
		if (c.resolve) {
			const cfg = resolveAzureConfig(c);
			baseURL = cfg.baseUrl;
			apiVersion = cfg.apiVersion;
		}
		out.resolvedBaseURL = baseURL;
		out.apiVersion = apiVersion;
		const client = new AzureOpenAI({
			apiKey: "k",
			apiVersion,
			dangerouslyAllowBrowser: true,
			maxRetries: 0,
			fetch,
			baseURL,
		});
		const s = await client.responses.create({ model: "m", input: [], stream: true });
		for await (const _ of s) {}
	} catch (e) {
		if (out.url === undefined) out.error = e.message;
	}
	return out;
};

const results = [];
for (const c of JSON.parse(input)) results.push(await run(c));
process.stdout.write(JSON.stringify(results));
