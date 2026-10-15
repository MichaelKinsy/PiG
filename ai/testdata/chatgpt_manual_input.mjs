// Run authorizationResultFromManualInput (private, with authorizationResultFromCallback and REDIRECT_URI cut from Pi's packages/ai/src/auth/oauth/openai-chatgpt.ts) over {input, state} probes from stdin.
// usage: node chatgpt_manual_input.mjs <path to openai-chatgpt.ts>
// Result: {ok: {code, clientId}} or {err: message}.
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
let text = "";
for (const name of ["CALLBACK_PORT", "CALLBACK_PATH", "REDIRECT_URI"]) {
	const m = new RegExp(`^const ${name}\\b.*$`, "m").exec(source);
	if (!m) throw new Error(`${name} not found`);
	text += `${m[0]}\n`;
}
for (const name of ["authorizationResultFromCallback", "authorizationResultFromManualInput"]) {
	const m = new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!m) throw new Error(`${name} not found`);
	text += `${m[0]}\n`;
}
const fn = new Function(`${module.stripTypeScriptTypes(text)}\nreturn authorizationResultFromManualInput;`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			try {
				return { ok: fn(p.input, p.state) };
			} catch (error) {
				return { err: error.message };
			}
		}),
	),
);
