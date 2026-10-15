// Drive the installed pi-ai faux provider, never a translated oracle: which text_delta chunk lengths a tokenSize {min?, max?} produces.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const faux = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/providers/faux.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const lengths = new Set();
  for (let run = 0; run < probe.runs; run++) {
    const core = faux.createFauxCore({ api: "faux-probe", provider: "faux-probe", ...(probe.tokenSize ? { tokenSize: probe.tokenSize } : {}) });
    core.setResponses([faux.fauxAssistantMessage([faux.fauxText("a".repeat(probe.chars))], { timestamp: 1 })]);
    const stream = core.stream(core.models[0], { messages: [] }, {});
    const deltas = [];
    for await (const event of stream) if (event.type === "text_delta") deltas.push(event.delta.length);
    deltas.pop();
    for (const length of deltas) lengths.add(length);
  }
  results.push([...lengths].sort((a, b) => a - b));
}
process.stdout.write(JSON.stringify(results));
