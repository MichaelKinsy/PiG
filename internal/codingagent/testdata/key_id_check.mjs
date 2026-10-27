// Check each JSON value of stdin against the pinned Pi's KeybindingValueSchema (key-id-schema.ts) with its own typebox and print [Check(value)...].
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Compile } = await import(pathToFileURL(createRequire(root + "package.json").resolve("typebox/compile")));
const { KeybindingValueSchema, KeyIdSchema } = await import(pathToFileURL(root + "dist/core/key-id-schema.js"));
const validator = Compile(KeybindingValueSchema);
const single = Compile(KeyIdSchema);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const values = JSON.parse(input);
process.stdout.write(JSON.stringify({ value: values.map((v) => validator.Check(v)), id: values.map((v) => single.Check(v)) }), () => process.exit(0));
