import { readFileSync } from "node:fs";

const root = new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.0") {
  throw new Error("Expected Pi 1.0.0");
}
const dist = new URL("node_modules/@earendil-works/pi-tui/dist/", root);
export const { Editor, wordWrapLine } = await import(new URL("components/editor.js", dist));
export const { visibleWidth } = await import(new URL("utils.js", dist));
export const theme = {
  borderColor: s => s,
  selectList: { selectedPrefix: s => s, selectedText: s => s, description: s => s, scrollInfo: s => s, noMatch: s => s },
};
export const testTUI = (columns = 80, rows = 24) => ({ terminal: { columns, rows }, requestRender() {} });
