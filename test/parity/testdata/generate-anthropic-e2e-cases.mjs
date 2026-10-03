// Generates ai/testdata/port-wave-13/anthropic_e2e_cases.json from the published pinned pi-ai catalog.
// The probe selection is packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85 and
// anthropic-long-cache-retention-e2e.test.ts:22-70 (identical selectors). Run from the repository root:
//   node test/parity/testdata/generate-anthropic-e2e-cases.mjs > ai/testdata/port-wave-13/anthropic_e2e_cases.json
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const dist = process.env.PIG_PUBLISHED_AI_DIST ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist");
const { getModels, getProviders } = await import(pathToFileURL(resolve(dist, "compat.js")).href);

const cases = getProviders().flatMap((provider) =>
	getModels(provider)
		.filter((model) => model.api === "anthropic-messages")
		.map((model) => ({ name: `${provider}/${model.id}`, provider, model })),
);

function getProbePriority(model) {
	const modelId = model.id.toLowerCase();
	let priority = model.cost.input + model.cost.output;
	if (modelId.includes("haiku") && (modelId.includes("4-5") || modelId.includes("4.5"))) priority -= 1000;
	else if (modelId.includes("sonnet") && (modelId.includes("4-") || modelId.includes("4."))) priority -= 750;
	else if (modelId.includes("claude") && (modelId.includes("4-") || modelId.includes("4."))) priority -= 500;
	return priority;
}

function selectOneCasePerProvider(testCases) {
	const byProvider = new Map();
	for (const testCase of testCases) {
		const providerCases = byProvider.get(testCase.provider) ?? [];
		providerCases.push(testCase);
		byProvider.set(testCase.provider, providerCases);
	}
	return Array.from(byProvider.values()).map(
		(providerCases) =>
			providerCases.sort((a, b) => getProbePriority(a.model) - getProbePriority(b.model) || a.model.id.localeCompare(b.model.id))[0],
	);
}

const names = (testCases) => testCases.map((testCase) => testCase.name);
const all = names(cases).sort();
const configured = names(selectOneCasePerProvider(cases));
const forcedEager = names(selectOneCasePerProvider(cases.filter((testCase) => testCase.model.compat?.supportsEagerToolInputStreaming !== false)));
process.stdout.write(`${JSON.stringify({ all, configured, forcedEager }, null, 2)}\n`);
