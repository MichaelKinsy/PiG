// Runs the 23 assigned editor.test.ts cases against the installed, pinned Pi implementation.
// Only TypeScript annotations and the terminal/theme construction are adapted.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../..");
const require = createRequire(path.join(root, "extensions/sdk-ts/package.json"));
const ts = require("typescript");
const sourcePath = path.join(root, ".upstream/current/packages/tui/test/editor.test.ts");
const source = ts.createSourceFile(sourcePath, fs.readFileSync(sourcePath, "utf8"), ts.ScriptTarget.Latest, true);
const selected = new Set([2096, 2135, 2212, 2233, 2255, 2288, 2338, 2379, 2423, 2475, 2508, 2567, 2600, 2652, 2689, 2729, 2785, 2836, 2885, 2931, 2994, 3019, 3042]);
const cases = [];
function walk(node) {
  if (ts.isCallExpression(node) && node.expression.getText(source) === "it") {
    const line = source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
    if (selected.delete(line)) cases.push(`// editor.test.ts:${line}\n${node.getText(source)};`);
  }
  ts.forEachChild(node, walk);
}
walk(source);
if (selected.size) throw new Error(`Missing original cases: ${[...selected]}`);
const pkg = path.join(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(fs.readFileSync(path.join(pkg, "package.json"), "utf8")).version !== "1.0.2") throw new Error("Pi version mismatch");
const dist = path.join(pkg, "node_modules/@earendil-works/pi-tui/dist");
const harness = `
import assert from "node:assert";
import {mkdirSync,mkdtempSync,rmSync,writeFileSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {it} from "node:test";
import {stripVTControlCharacters} from "node:util";
import {Editor} from ${JSON.stringify(path.join(dist, "components/editor.js"))};
import {CombinedAutocompleteProvider} from ${JSON.stringify(path.join(dist, "autocomplete.js"))};
import {TuiMainScreen} from ${JSON.stringify(path.join(dist, "tui-main-screen.js"))};
const identity = s => s;
const defaultEditorTheme = {borderColor:identity,selectList:{selectedPrefix:identity,selectedText:identity,description:identity,scrollInfo:identity,noMatch:identity}};
function createTestTUI() {return new TuiMainScreen({columns:80,rows:24,kittyProtocolActive:true,write(){},start(){},stop(){},hideCursor(){},showCursor(){},moveBy(){},clearLine(){},clearFromCursor(){},clearScreen(){},setTitle(){},setProgress(){}});}
${source.statements.find(n => ts.isFunctionDeclaration(n) && n.name?.text === "applyCompletion").getText(source)}
${source.statements.find(n => ts.isFunctionDeclaration(n) && n.name?.text === "flushAutocomplete").getText(source)}
`;
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-editor-completion-"));
try {
  const file = path.join(dir, "original.test.mjs");
  fs.writeFileSync(file, ts.transpileModule(harness + cases.join("\n"), {compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText);
  const result = spawnSync(process.execPath, ["--test", file], {stdio:"inherit"});
  process.exitCode = result.status ?? 1;
} finally {
  fs.rmSync(dir, {recursive:true,force:true});
}
