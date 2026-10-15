// Drive the installed Pi's attachJsonlLineReader over chunked byte streams, never a translated oracle.
import { EventEmitter } from "node:events";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { attachJsonlLineReader, serializeJsonLine } = await import(pathToFileURL(root + "dist/modes/rpc/jsonl.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map(({ chunks, values }) => {
  const stream = new EventEmitter();
  const lines = [];
  attachJsonlLineReader(stream, (line) => lines.push(line));
  for (const hex of chunks) stream.emit("data", Buffer.from(hex, "hex"));
  stream.emit("end");
  return { lines, serialized: values.map((v) => serializeJsonLine(JSON.parse(v))) };
})));
