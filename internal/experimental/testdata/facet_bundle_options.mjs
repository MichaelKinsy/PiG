// Compare the installed Pi implementation, never a translated test oracle.
import { mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = join(process.env.PIG_TEST_ROOT, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/");
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { bundleFacets } = await import(pathToFileURL(root + "node_modules/@earendil-works/chord/dist/bundler.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const directory = mkdtempSync(join(tmpdir(), "facet-options-"));
writeFileSync(join(directory, "entry.js"), process.argv[3] === undefined ? "" : "");
const results = [];
for (const [index, probe] of JSON.parse(input).entries()) {
  writeFileSync(join(directory, "entry.js"), probe.source);
  const options = { plugin: { id: "p" }, entries: { e: "entry.js" }, outdir: `out-${index}`, workingDirectory: directory };
  for (const key of ["minify", "define", "platform", "target"]) if (probe[key] !== undefined) options[key] = probe[key];
  try {
    const result = await bundleFacets(options);
    const file = result.manifest.entries.e.file;
    results.push({ ok: true, file, code: readFileSync(join(directory, `out-${index}`, file), "utf8") });
  } catch (error) {
    results.push({ ok: false, error: error.message });
  }
}
process.stdout.write(JSON.stringify(results));
