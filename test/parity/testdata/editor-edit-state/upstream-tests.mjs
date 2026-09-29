import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { describe, it } from "node:test";
import { Editor, visibleWidth, theme, testTUI } from "../editor-wrap-helper/pi-runtime.mjs";

const source = readFileSync(new URL("../../../../.upstream/current/packages/tui/test/editor.test.ts", import.meta.url), "utf8");
const editing = source.slice(source.indexOf('\tdescribe("Kill ring",'), source.indexOf('\t\tit("undoes autocomplete",'));
const navigation = source.slice(source.indexOf('\tdescribe("Character jump (Ctrl+])",'), source.lastIndexOf("\n});"));
const bodies = stripTypeScriptTypes(editing + "\n});\n" + navigation);
new Function("assert", "describe", "it", "Editor", "visibleWidth", "defaultEditorTheme", "createTestTUI", "applyCompletion", bodies)(
  assert, describe, it, Editor, visibleWidth, theme, testTUI, () => { throw new Error("Unexpected autocomplete application in editing tests"); },
);
