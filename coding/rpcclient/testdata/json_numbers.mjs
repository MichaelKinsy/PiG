// JSON.stringify of numbers and number-bearing values under the installed Node, as Pi's serializeJsonLine frames them.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((token) => {
  const value = token === "NaN" ? NaN : token === "Infinity" ? Infinity : token === "-Infinity" ? -Infinity : token === "-0" ? -0 : Number(token);
  return JSON.stringify({ v: value, a: [value], s: String(value) }) + "\n";
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
