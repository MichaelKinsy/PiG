// Reads a JSON array of JSON texts on stdin and prints JSON.stringify(JSON.parse(text)) for each.
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
process.stdout.write(JSON.stringify(JSON.parse(text).map((source) => JSON.stringify(JSON.parse(source)) ?? "undefined")));
