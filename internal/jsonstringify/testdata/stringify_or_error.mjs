// Reads a JSON array of JSON texts on stdin and prints, for each, JSON.stringify(JSON.parse(text)), or null when JSON.parse throws.
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
process.stdout.write(JSON.stringify(JSON.parse(text).map((source) => {
	try {
		return JSON.stringify(JSON.parse(source)) ?? "undefined";
	} catch {
		return null;
	}
})));
