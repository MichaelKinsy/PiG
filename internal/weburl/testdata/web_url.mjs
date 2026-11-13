// Pi's utils/web-url.ts (webUrl and displayUrl) with the pi-tui replaceControlCharacters pattern, run on the qualified Node.
// Reads a JSON array of strings on stdin; writes [{"webUrl": string|null, "displayUrl": string}] for each.
const replaceControlCharacters = (value) => value.replace(/[\u0000-\u001f\u007f-\u009f]+/g, " ");

function webUrl(value) {
	if (!URL.canParse(value)) return undefined;
	const url = new URL(value);
	return url.protocol === "https:" || url.protocol === "http:" ? url.href : undefined;
}

function displayUrl(value) {
	return webUrl(value) ?? replaceControlCharacters(value);
}

process.stdin.setEncoding("utf8");
let input = "";
for await (const chunk of process.stdin) input += chunk;
const values = JSON.parse(input);
process.stdout.write(JSON.stringify(values.map((value) => ({ webUrl: webUrl(value) ?? null, displayUrl: displayUrl(value) }))));
