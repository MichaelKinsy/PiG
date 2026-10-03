import { readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const packageRoot = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(readFileSync(join(packageRoot, "package.json"), "utf8")).version !== "1.0.0") throw new Error("unexpected Pi version");
const { createMarkdownTransform } = await import(pathToFileURL(join(packageRoot, "dist/modes/interactive/components/markdown-transform.js")));
const replacement = [];
const transform = createMarkdownTransform("assistant", false, [
  text => { replacement.push(`B ${text}`); return text; },
  text => { replacement.push(`A ${text} entered`); if (text === "first") replacement.push("A first returned"); return text; },
  text => { replacement.push(`C ${text}`); return text; },
]);
transform("first", 80);
transform("second", 80);
console.log(`replacement:${JSON.stringify(replacement)}`);
const components = [];
const capture = text => { components.push(text); return `transformed ${text}`; };
const assistant = createMarkdownTransform("assistant", false, [capture]);
const user = createMarkdownTransform("user", false, [capture]);
assistant("A1", 80);
user("B1", 80);
assistant("A2", 80);
console.log(`components:${JSON.stringify(components)}`);
