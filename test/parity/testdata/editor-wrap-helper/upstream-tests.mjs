import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { describe, it } from "node:test";
import { stripVTControlCharacters } from "node:util";
import { Editor, wordWrapLine, visibleWidth, theme, testTUI } from "./pi-runtime.mjs";

// Execute the assigned, unchanged Pi test bodies; erase only TypeScript syntax.
const source = readFileSync(new URL("../../../../.upstream/current/packages/tui/test/editor.test.ts", import.meta.url), "utf8");
const start = source.indexOf('\tdescribe("Scroll indicators",');
const end = source.indexOf('\tdescribe("Kill ring",', start);
assert.ok(start >= 0 && end > start);
const bodies = stripTypeScriptTypes(source.slice(start, end));
new Function("assert", "describe", "it", "stripVTControlCharacters", "Editor", "wordWrapLine", "visibleWidth", "defaultEditorTheme", "createTestTUI", bodies)(
  assert, describe, it, stripVTControlCharacters, Editor, wordWrapLine, visibleWidth, theme, testTUI,
);
