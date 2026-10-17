// Run the Gemini model-id helpers of Pi's packages/ai/src/api/google-shared.ts (usesGoogleThinkingLevel, getGeminiMajorVersion, requiresToolCallId, supportsMultimodalFunctionResponse, supportsGoogleStrictToolSampling,
// cut from the source) over model ids from stdin. Result per id: {thinkingLevel, major (number as a string or null), toolCallId, multimodal, strict}.
// usage: node google_model_ids.mjs <path to google-shared.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const names = ["usesGoogleThinkingLevel", "getGeminiMajorVersion", "requiresToolCallId", "supportsMultimodalFunctionResponse", "supportsGoogleStrictToolSampling"];
let text = "";
for (const name of names) {
	const m = new RegExp(`^(?:export )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!m) throw new Error(`${name} not found`);
	text += `${m[0].replace(/^export /, "")}\n`;
}
const fns = new Function(`${module.stripTypeScriptTypes(text)}\nreturn { ${names.join(", ")} };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((id) => {
			const major = fns.getGeminiMajorVersion(id);
			return {
				thinkingLevel: fns.usesGoogleThinkingLevel({ id }),
				major: major === undefined ? null : String(major),
				toolCallId: fns.requiresToolCallId(id),
				multimodal: fns.supportsMultimodalFunctionResponse(id),
				strict: fns.supportsGoogleStrictToolSampling(id),
			};
		}),
	),
);
