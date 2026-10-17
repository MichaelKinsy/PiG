// Compile generated JSON Schemas with the installed Pi's typebox and print Check() for each document: [{schema, documents}] -> boolean[][].
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Compile } = await import(pathToFileURL(createRequire(root + "package.json").resolve("typebox/compile")));
const cases = JSON.parse(readFileSync(process.argv[3], "utf8"));
const out = cases.map(({ schema, documents }) => {
  const validator = Compile(schema);
  return documents.map((document) => validator.Check(document));
});
process.stdout.write(JSON.stringify(out), () => process.exit(0));
