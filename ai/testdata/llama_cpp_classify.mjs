// Drive the installed pi-ai llama-cpp-classify classifier against a scripted llama-server, never a translated oracle. The server's answers are a pure function of the
// request and the probe, so the Go side runs the same script and the two sides see the same exchange.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { classify } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/api/llama-cpp-classify.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const ORIGIN = "http://llama.test";
const wordID = (word) => 1000 + ([...word].reduce((sum, ch, i) => sum + ch.codePointAt(0) * (i + 1), 0) % 50000);
const LABELS = [..."ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "Yes", "No"];

// tokenize is the scripted tokenizer: "words" gives a newline, a run of letters and digits, or any other character one token each; "merged" also fuses a newline with the word after it;
// "chars" gives every code point its own token; "objects" is "words" with each token an object; "bad", "string" and "shared" are malformed or colliding answers.
function tokenize(mode, content) {
	if (mode === "bad") return "not a list";
	if (mode === "string") return ["7"];
	if (mode === "shared") return [10, 55].slice(0, content === "\n" ? 1 : 2);
	if (mode === "chars") return [...content].map((ch) => ch.codePointAt(0));
	const words = content.match(/\n|[A-Za-z0-9]+|[^A-Za-z0-9\n]/gu) ?? [];
	if (mode === "merged" && words[0] === "\n" && words.length > 1) return [2000 + wordID(words.slice(1).join(""))];
	const ids = words.map((word) => (word === "\n" ? 10 : /^[A-Za-z0-9]+$/u.test(word) ? wordID(word) : word.codePointAt(0)));
	return mode === "objects" ? ids.map((id) => ({ id, text: "t" })) : ids;
}
// labelIDs maps every answer label to the token the scripted model emits for it after a newline.
function labelIDs(mode) {
	const ids = new Map();
	for (const label of LABELS) {
		const tokens = tokenize(mode, `\n${label}`);
		const first = tokens[tokens.length - 1];
		ids.set(label, typeof first === "object" ? first.id : first);
	}
	return ids;
}

const results = [];
for (const [index, probe] of probes().entries()) {
	const requests = [];
	const ids = labelIDs(probe.tokenizer);
	const fetch = async (url, init) => {
		const path = url.slice(ORIGIN.length);
		const body = JSON.parse(init.body);
		requests.push({ path, authorization: init.headers.authorization ?? null, contentType: init.headers["content-type"] ?? null, body: init.body });
		const reply = (status, text) => new Response(text, { status, headers: { "content-type": "application/json" } });
		if (path === "/tokenize") return reply(200, JSON.stringify({ tokens: tokenize(probe.tokenizer, body.content) }));
		if (path === "/apply-template") return reply(probe.template.status, probe.template.body);
		const top = [];
		for (const [label, rank] of Object.entries(probe.ranks)) if (body.n_probs >= rank.depth) top.push({ id: ids.get(label), token: label, logprob: JSON.parse(rank.logprob) });
		top.push(...(probe.noise ?? []).map((raw) => JSON.parse(raw)));
		const shape = probe.shape;
		if (shape === "array") return reply(200, "[]");
		if (shape === "noProbs") return reply(200, "{}");
		if (shape === "emptyList") return reply(200, JSON.stringify({ completion_probabilities: [] }));
		if (shape === "noTop") return reply(200, JSON.stringify({ completion_probabilities: [{ id: 1 }] }));
		return reply(200, JSON.stringify({ completion_probabilities: [{ top_logprobs: top }] }));
	};
	const model = { id: `m${index}`, name: "M", api: "llama-cpp-classify", provider: "p", baseUrl: ORIGIN + probe.baseSuffix, input: probe.images ? ["text", "image"] : ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000 };
	const questions = {};
	for (const q of probe.questions) {
		if (q.type === "choice") questions[q.id] = { type: "choice", instructions: q.instructions, criteria: Object.fromEntries((q.criteria ?? []).map((c) => [c.key, c.description])) };
		else if (q.type === "score") questions[q.id] = { type: "score", instructions: q.instructions, criteria: q.levels ?? [] };
		else questions[q.id] = { type: "bool", instructions: q.instructions, criteria: { true: q.yes, false: q.no } };
	}
	const options = { apiKey: probe.apiKey || undefined, fetch };
	if (probe.temperature !== null) options.temperature = probe.temperature;
	const context = { state: JSON.parse(probe.state), questions };
	if (probe.withImages) context.images = [{ type: "image", data: "AAAA", mimeType: "image/png" }];
	const result = await classify(model, context, options);
	results.push({ requests, stopReason: result.stopReason, errorMessage: result.errorMessage ?? null, answersJson: JSON.stringify(result.answers) });
}
process.stdout.write(JSON.stringify(results));

function probes() { return JSON.parse(input); }
