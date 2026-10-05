import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const packageRoot = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(readFileSync(join(packageRoot, "package.json"), "utf8")).version !== "1.0.3") throw new Error("unexpected Pi version");
const { discoverAndLoadExtensions, loadExtensions } = await import(pathToFileURL(join(packageRoot, "dist/core/extensions/loader.js")));
const { ExtensionRunner } = await import(pathToFileURL(join(packageRoot, "dist/core/extensions/runner.js")));
const fixture = resolve("test/parity/scenarios/extensions-runtime/testdata/runner-markdown");
const root = mkdtempSync(join(tmpdir(), "runner-markdown-"));
try {
  mkdirSync(join(root, "extensions"));
  for (const suffix of ["a", "b"]) copyFileSync(join(fixture, "identity.ts"), join(root, "extensions", `markdown-renderer-${suffix}.ts`));
  const discovered = await discoverAndLoadExtensions([], root, root);
  const ordered = await loadExtensions([join(fixture, "first.ts"), join(fixture, "second.ts")], root);
  const reversed = await loadExtensions([join(fixture, "second.ts"), join(fixture, "first.ts")], root);
  for (const [name, result] of [["identity", discovered], ["ordered", ordered], ["reversed", reversed]]) {
    if (result.errors.length) throw new Error(JSON.stringify(result.errors));
    const runner = new ExtensionRunner(result.extensions, result.runtime, root, undefined, undefined);
    const transformers = runner.getMarkdownTransformers();
    let output = "x";
    for (const transform of transformers) output = transform(output, {messageType:"assistant", isStreaming:true, availableWidth:73});
    console.log(`${name}:${transformers.length}:${output}`);
    result.runtime.invalidate();
  }
} finally {
  rmSync(root, {recursive:true, force:true});
}
