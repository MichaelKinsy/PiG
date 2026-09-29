import { readFileSync } from "node:fs";
import { Editor, theme, testTUI } from "./pi-runtime.mjs";

const cases = JSON.parse(readFileSync(new URL("./cases.json", import.meta.url), "utf8"));
cases.push({ name: "wide overflow after backtrack", width: 188, padding: 0, text: " " + "a".repeat(186) + "你", keys: [] });
cases.push({ name: "non-CJK backtrack overflow", width: 188, padding: 0, text: " " + "a".repeat(186) + "✅", keys: [] });
cases.push({ name: "UTF16 wrap offsets", width: 4, padding: 0, text: "A😀B😀C", keys: [] });
// Exercise both centering thresholds and ellipsis lengths through the real renderer.
for (let width = 2; width <= 42; width++) cases.push({ ...cases[1], name: `scroll width ${width}`, width });
const hex = s => Buffer.from(s).toString("hex");
const results = [];
for (const { name, text, width, padding, keys } of cases) {
  const e = new Editor(testTUI(width), { ...theme, borderColor: s => `\x1b[35m${s}\x1b[39m` }, { paddingX: padding });
  e.setText(text);
  const states = [];
  const capture = () => states.push({ text: hex(e.getText()), cursor: e.getCursor(), rows: e.render(width).map(hex) });
  capture();
  for (let key of keys) {
    if (key === "PASTE20" || key === "PASTE30") key = "\x1b[200~" + Array(Number(key.slice(5))).fill("line").join("\n") + "\x1b[201~";
    e.handleInput(key);
    capture();
  }
  results.push({ name, states });
}
console.log(JSON.stringify(results));
