import { readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';
const root = fileURLToPath(new URL('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent', import.meta.url));
if (JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version !== '0.99.2') throw new Error('Expected Pi 0.99.2');
const { Editor } = await import(pathToFileURL(join(root, 'node_modules/@earendil-works/pi-tui/dist/components/editor.js')));
const theme = {borderColor:s=>s,selectList:{selectedPrefix:s=>s,selectedText:s=>s,description:s=>s,scrollInfo:s=>s,noMatch:s=>s}};
const tui = {terminal:{columns:80,rows:24},requestRender(){}};
const hex = s => Buffer.from(s).toString('hex');
const rows = [];
for (const [name,input] of [
  ['empty',''],
  ['ordinary',' \t ordinary prompt \n'],
  ['BOM edges','\ufeffprompt\ufeff'],
  ['BOM only','\ufeff'],
  ['NEL edges','\u0085prompt\u0085'],
  ['NEL only','\u0085'],
  ['mixed edges','\ufeff \u0085prompt\u0085 \ufeff'],
]) {
  const historyEditor = new Editor(tui,theme);
  historyEditor.addToHistory('older');
  historyEditor.addToHistory(input);
  historyEditor.handleInput('\x1b[A');
  const history = [hex(historyEditor.getText())];
  historyEditor.handleInput('\x1b[A');
  history.push(hex(historyEditor.getText()));
  const editor = new Editor(tui,theme);
  const submitted = [];
  editor.onSubmit = text => submitted.push(hex(text));
  editor.setText(input);
  editor.handleInput('\r');
  rows.push({name,input:hex(input),history,submitted,remaining:hex(editor.getText())});
}
const editor = new Editor(tui,theme);
editor.addToHistory('older');
editor.addToHistory('prompt');
editor.addToHistory('\ufeffprompt\ufeff');
const deduplicated = [];
for (let i=0;i<3;i++) { editor.handleInput('\x1b[A'); deduplicated.push(hex(editor.getText())); }
console.log(JSON.stringify({rows,deduplicated}));
