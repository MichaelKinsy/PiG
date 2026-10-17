// Prints JSON.parse's outcome for each input read as a JSON array of strings on stdin.
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
const out = JSON.parse(text).map((input) => {
	try {
		JSON.parse(input);
		return null;
	} catch (error) {
		return error instanceof SyntaxError ? error.message : `non-SyntaxError ${error}`;
	}
});
process.stdout.write(JSON.stringify(out));
