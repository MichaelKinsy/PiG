import { readFileSync } from "node:fs";
import { Editor, theme, testTUI } from "../editor-wrap-helper/pi-runtime.mjs";
const cases = JSON.parse(readFileSync(new URL("./cases.json", import.meta.url), "utf8"));
const hex = s => Buffer.from(s).toString("hex");
const results = cases.map(tc => {
  const e = new Editor(testTUI(), theme);
  const submitted = [];
  e.onSubmit = text => submitted.push(hex(text));
  const states = [];
  const capture = () => states.push({ text: hex(e.getText()), expanded: hex(e.getExpandedText()), cursor: e.getCursor(), rows: e.render(tc.width ?? 30).map(hex), submitted: [...submitted] });
  capture();
  for (const [op, value] of tc.steps) {
    switch (op) {
      case "set": e.setText(value); break;
      case "key": e.handleInput(value); break;
      case "insert": e.insertTextAtCursor(value); break;
      case "history": e.addToHistory(value); break;
      case "paste": e.handleInput(`\x1b[200~${value}\x1b[201~`); break;
      case "bigPaste": e.handleInput(`\x1b[200~${Array.from({length: 12}, (_, i) => `${value}${i}`).join("\n")}\x1b[201~`); break;
      case "type": for (const ch of value) { e.handleInput(ch); capture(); } break;
      case "right": for (let i=0;i<Number(value);i++) { e.handleInput("\x1b[C"); capture(); } break;
      default: throw new Error(op);
    }
    capture();
  }
  return { name: tc.name, states };
});
console.log(JSON.stringify(results));
