// Drive one of Pi's file tools (core/tools/<name>.ts) with its default operations on a real directory and print the text it returns or the message it throws. argv: version, tool (ls|grep|find), directory, arguments as JSON.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const name = process.argv[3];
const mod = await import(pathToFileURL(root + `dist/core/tools/${name}.js`));
const tool = mod[`create${name[0].toUpperCase()}${name.slice(1)}Tool`](process.argv[4]);
let out;
try {
  const r = await tool.execute("call", JSON.parse(process.argv[5]));
  out = { text: r.content.map((c) => c.text).join("") };
} catch (error) { out = { thrown: String(error && error.message) }; }
process.stdout.write(JSON.stringify(out), () => process.exit(0));
