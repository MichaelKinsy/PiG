// JSON.parse then JSON.stringify of each document: what Pi's JSON gives for a message with a lone surrogate (the escape
// `\ud83d`) and `<`, `>`, `&` raw. stdin: a JSON array of documents (strings). stdout: a JSON array of the results.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((doc) => JSON.stringify(JSON.parse(doc)))));
