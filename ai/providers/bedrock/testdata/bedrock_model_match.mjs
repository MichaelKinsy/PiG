// Run Pi's private Bedrock model-matching helpers, cut from packages/ai/src/api/bedrock-converse-stream.ts, over {id, name, forceCache} probes from stdin: getModelMatchCandidates, supportsAdaptiveThinking,
// supportsNativeXhighEffort, supportsThinkingBlockBinding, isAnthropicClaudeModel and supportsPromptCaching (getProviderEnvValue is stubbed to read the env object).
// usage: node bedrock_model_match.mjs <path to bedrock-converse-stream.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const names = ["getModelMatchCandidates", "supportsAdaptiveThinking", "supportsNativeXhighEffort", "supportsThinkingBlockBinding", "isAnthropicClaudeModel", "supportsPromptCaching"];
let text = "const getProviderEnvValue = (name, env) => env?.[name];\n";
for (const name of names) {
	const m = new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!m) throw new Error(`${name} not found`);
	text += `${m[0]}\n`;
}
const fns = new Function(`${module.stripTypeScriptTypes(text)}\nreturn { ${names.join(", ")} };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			const model = { id: p.id, ...(p.name === null ? {} : { name: p.name }) };
			const env = p.forceCache ? { AWS_BEDROCK_FORCE_CACHE: "1" } : {};
			return {
				candidates: fns.getModelMatchCandidates(model.id, model.name),
				adaptive: fns.supportsAdaptiveThinking(model.id, model.name),
				xhigh: fns.supportsNativeXhighEffort(model),
				binding: fns.supportsThinkingBlockBinding(model),
				claude: fns.isAnthropicClaudeModel(model),
				caching: fns.supportsPromptCaching(model, env),
			};
		}),
	),
);
