// Load each models.json document (argv[3]: a JSON file holding an array of document texts) with Pi's ModelConfig.load and print, per
// document, the schema errors it reports ("  - path: message" lines joined by newlines) or null when the document validates.
import { mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const piCodingAgent = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(piCodingAgent + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ModelConfig } = await import(pathToFileURL(piCodingAgent + "dist/core/model-config.js"));
const documents = JSON.parse(readFileSync(process.argv[3], "utf8"));
const dir = mkdtempSync(join(tmpdir(), "pig-models-json-"));
const out = [];
try {
  for (const [index, text] of documents.entries()) {
    const path = join(dir, `models-${index}.json`);
    writeFileSync(path, text);
    const error = (await ModelConfig.load(path)).getError();
    const prefix = "Invalid models.json schema:\n";
    const suffix = `\n\nFile: ${path}`;
    if (error === undefined) out.push(null);
    else if (error.startsWith(prefix) && error.endsWith(suffix)) out.push(error.slice(prefix.length, error.length - suffix.length));
    else throw new Error(`document ${index}: not a schema error: ${error}`);
  }
} finally {
  rmSync(dir, { recursive: true, force: true });
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
