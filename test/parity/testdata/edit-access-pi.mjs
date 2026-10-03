import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.0");
const { createEditTool } = await import(pathToFileURL(join(root, "dist/core/tools/edit.js")));
const controller = new AbortController();
let read = false;
const tool = createEditTool(process.cwd(), { operations: {
  access: async () => { controller.abort(); },
  readFile: async () => { read = true; return Buffer.from("hello"); },
  writeFile: async () => { throw new Error("write after abort"); },
}});
let message;
try {
  await tool.execute("call", { path: "f", edits: [{ oldText: "hello", newText: "world" }] }, controller.signal);
} catch (error) { message = error.message; }
assert.equal(message, "Operation aborted");
assert.equal(read, false);
console.log("EDIT_ACCESS " + JSON.stringify([message, read]));
