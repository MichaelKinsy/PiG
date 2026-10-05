// Published Pi Input is the oracle, including original test bodies and raw UTF-16 units.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";
const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.2");
const tuiEntry = import.meta.resolve("@earendil-works/pi-tui", pathToFileURL(join(root, "package.json")).href);
const tuiRoot = fileURLToPath(new URL("./", tuiEntry));
const { Input } = await import(tuiEntry);
const moduleURL = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64");
const tests = [];
globalThis.__pigInputRenameTests = tests;
const framework = moduleURL(`export const describe=(_name,run)=>run(),it=(name,run)=>globalThis.__pigInputRenameTests.push({name,run});`);
try {
  for (const [file, marker] of [["input", "INPUT_ORIGINAL_TESTS"], ["word-navigation", "WORD_ORIGINAL_TESTS"]]) {
    tests.length = 0;
    const source = readFileSync(`.upstream/current/packages/tui/test/${file}.test.ts`, "utf8");
    let code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText;
    code = code.replace(/from "([^"]+)"/g, (_, specifier) => `from ${JSON.stringify(specifier === "node:test" ? framework : specifier.startsWith("../src/") ? pathToFileURL(join(tuiRoot, specifier.slice(7).replace(/\.ts$/, ".js"))).href : specifier)}`);
    await import(moduleURL(code));
    assert.equal(tests.length, source.split("\n").filter(line => /\bit\(/.test(line)).length);
    for (const test of tests) await test.run();
    console.log(marker + " ok");
  }
} finally { delete globalThis.__pigInputRenameTests; }
for (const [name, initial, replacement, key, want] of [
  ["fresh", "", "Old", "X", "XOld"],
  ["retained", "a", "Old", "X", "OXld"],
  ["clamped", "long", "a", "X", "aX"],
  ["empty", "a", "", "X", "X"],
  ["astral prefix", "😀", "abcd", "X", "abXcd"],
  ["split pair", "a", "😀", "X", "\ud83dX\ude00"],
  ["backspace half", "a", "😀", "\x7f", "\ude00"],
  ["delete half", "a", "😀", "\x1b[3~", "\ud83d"],
]) {
  const input = new Input();
  input.handleInput(initial);
  input.setValue(replacement);
  input.handleInput(key);
  assert.equal(input.getValue(), want);
  console.log("INPUT_UTF16 " + JSON.stringify([name, Array.from({ length: input.getValue().length }, (_, i) => input.getValue().charCodeAt(i))]));
}
