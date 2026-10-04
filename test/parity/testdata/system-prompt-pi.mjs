import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.1");
const { buildSystemPrompt, buildSystemPromptSections } = await import(pathToFileURL(join(root, "dist/core/system-prompt.js")));
const toolSnippets = { read: "Read file contents", bash: "Execute bash commands", edit: "Make surgical edits", write: "Create or overwrite files" };
const values = [
  buildSystemPromptSections({ cwd: "/tmp", toolSnippets }).tools,
  buildSystemPromptSections({ cwd: "/tmp", toolSnippets, selectedTools: [] }).tools,
  buildSystemPrompt({ cwd: "/tmp", forceSystemPrompt: "exact" }),
  buildSystemPrompt({ cwd: "/tmp", forceSystemPrompt: "" }),
];
console.log("SYSTEM_PROMPT " + JSON.stringify(values));
