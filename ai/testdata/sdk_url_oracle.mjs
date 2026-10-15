// Build the request URL the pinned provider SDKs build for a base URL (the SDKs in ./sdk, installed with npm ci --ignore-scripts) and report what global fetch does with it.
// Input (stdin): [{api, baseURL}]; output per case: {url, creds} where url is the string the SDK hands to fetch and creds says `new Request(url)` throws (global fetch refuses a URL with credentials), or {error} with the SDK's message when it fails before fetch.
// usage: node sdk_url_oracle.mjs
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import path from "node:path";
const sdk = path.join(import.meta.dirname, "sdk", "node_modules");
const { GoogleGenAI } = await import(pathToFileURL(path.join(sdk, "@google", "genai", "dist", "node", "index.mjs")).href);
const { default: Anthropic } = await import(pathToFileURL(path.join(sdk, "@anthropic-ai", "sdk", "index.mjs")).href);
const { default: OpenAI } = await import(pathToFileURL(path.join(sdk, "openai", "index.mjs")).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const stream = () => new Response("data: [DONE]\n\n", { headers: { "content-type": "text/event-stream" } });
const run = async ({ api, baseURL }) => {
	const out = {};
	const fetch = async (url) => {
		out.url = String(url);
		try { new Request(url); } catch { out.creds = true; }
		return stream();
	};
	try {
		switch (api) {
			case "openai-completions": {
				const client = new OpenAI({ apiKey: "k", baseURL, dangerouslyAllowBrowser: true, maxRetries: 0, fetch });
				const s = await client.chat.completions.create({ model: "m", messages: [], stream: true });
				for await (const _ of s) {}
				break;
			}
			case "openai-responses": {
				const client = new OpenAI({ apiKey: "k", baseURL, dangerouslyAllowBrowser: true, maxRetries: 0, fetch });
				const s = await client.responses.create({ model: "m", input: [], stream: true });
				for await (const _ of s) {}
				break;
			}
			case "openrouter-images": {
				const client = new OpenAI({ apiKey: "k", baseURL, dangerouslyAllowBrowser: true, maxRetries: 0, fetch: async (url) => { out.url = String(url); try { new Request(url); } catch { out.creds = true; } return new Response("{}", { headers: { "content-type": "application/json" } }); } });
				await client.chat.completions.create({ model: "m", messages: [], modalities: ["image", "text"] });
				break;
			}
			case "anthropic-messages": {
				const client = new Anthropic({ apiKey: "k", baseURL, dangerouslyAllowBrowser: true, maxRetries: 0, fetch });
				const s = await client.beta.messages.create({ model: "m", max_tokens: 1, messages: [], stream: true });
				for await (const _ of s) {}
				break;
			}
			case "pi-messages": {
				// pi-messages.ts:370,392 hands `new URL(...)` to options.fetch ?? the global fetch.
				const url = new URL(`${baseURL.replace(/\/+$/u, "")}/messages`);
				out.url = url.toString();
				try { new Request(url); } catch { out.creds = true; }
				return out;
			}
			case "google-generative-ai": {
				// @google/genai calls the global fetch; the adapter refuses a custom one. A URL with credentials makes fetch throw, which is the error the stream reports.
				globalThis.fetch = async (url) => {
					out.url = String(url);
					new Request(url);
					return new Response('data: {"candidates":[]}\n\n', { headers: { "content-type": "text/event-stream" } });
				};
				const client = new GoogleGenAI({ apiKey: "k", httpOptions: baseURL ? { baseUrl: baseURL, apiVersion: "" } : undefined });
				try {
					const s = await client.models.generateContentStream({ model: "m", contents: "hi" });
					for await (const _ of s) {}
				} catch (e) {
					out.error = e.message;
				}
				return out;
			}
			default:
				throw new Error(`unknown api ${api}`);
		}
	} catch (e) {
		if (out.url === undefined) out.error = e.message;
	}
	return out;
};
const results = [];
for (const c of JSON.parse(input)) results.push(await run(c));
process.stdout.write(JSON.stringify(results));
