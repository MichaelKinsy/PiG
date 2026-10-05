// Pi 0.87.1 TuiBase.handleTerminalInput owns the same focus boundary as Pig's interactive driver.
import assert from 'node:assert/strict';
import { readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? realpathSync('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.0.2');
const dist = join(root, 'node_modules/@earendil-works/pi-tui/dist');
const { TuiMainScreen } = await import(pathToFileURL(join(dist, 'tui-main-screen.js')).href);
const { TuiAltScreen } = await import(pathToFileURL(join(dist, 'tui-alt-screen.js')).href);
for (const Renderer of [TuiMainScreen, TuiAltScreen]) {
 let receive;
 const terminal = { columns: 80, rows: 24, start(input) { receive = input; }, stop() {}, write() {}, hideCursor() {}, showCursor() {} };
 const ui = new Renderer(terminal);
 const record = () => ({ focused: false, inputs: [], render() { return ['overlay']; }, invalidate() {}, handleInput(data) { this.inputs.push(data); this.onInput?.(data); } });
 const editor = record(), overlay = record(), replacement = record();
 ui.setFocus(editor);
 ui.start();
 try {
  ui.showOverlay(overlay);
  ui.setFocus(editor);
  receive('x');
  overlay.onInput = data => { if (data === 'b') ui.setFocus(replacement); };
  replacement.onInput = data => { if (data === '\r') ui.setFocus(editor); };
  for (const data of ['b', '1', '\r', 'y']) receive(data);
  assert.equal(ui.getFocusedComponent(), overlay);
  process.stdout.write(JSON.stringify({ Editor: editor.inputs, Overlay: overlay.inputs, Replacement: replacement.inputs }) + '\n');
 } finally { ui.stop(); }
}
