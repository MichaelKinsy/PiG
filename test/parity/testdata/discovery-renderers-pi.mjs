import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join, basename } from "node:path";
import { pathToFileURL } from "node:url";

const packageRoot = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(readFileSync(join(packageRoot, "package.json"), "utf8")).version !== "1.0.0") throw new Error("unexpected Pi version");
const { discoverAndLoadExtensions } = await import(pathToFileURL(join(packageRoot, "dist/core/extensions/loader.js")));
const root = mkdtempSync(join(tmpdir(), "discovery-renderers-"));
try {
  mkdirSync(join(root, "extensions"));
  copyFileSync("test/parity/scenarios/extensions-runtime/testdata/discovery-renderers/with-renderer.ts", join(root, "extensions", "with-renderer.ts"));
  const result = await discoverAndLoadExtensions([], root, root);
  if (result.errors.length || result.extensions.length !== 1) throw new Error(JSON.stringify(result.errors));
  const extension = result.extensions[0];
  console.log(JSON.stringify({ errors: result.errors.length, paths: [basename(extension.path)], markdown: typeof extension.markdownTransformer === "function", message: extension.messageRenderers.has("my-custom-type"), entry: extension.entryRenderers?.has("my-entry-type") === true }));
  result.runtime.invalidate();
} finally {
  rmSync(root, { recursive: true, force: true });
}
