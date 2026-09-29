// Pi 0.87.1 interactive-mode-status.test.ts:236, driven through the actual showExtensionCustom and TUI focus state machine.
import assert from 'node:assert/strict';
import { readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? realpathSync('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '0.87.1');
const load = path => import(pathToFileURL(join(root, path)).href);
const { InteractiveMode } = await load('dist/modes/interactive/interactive-mode.js');
const { Container, TuiMainScreen } = await load('node_modules/@earendil-works/pi-tui/dist/index.js');
let receive;
const terminal = { columns: 80, rows: 24, start(input) { receive = input; }, stop() {}, write() {}, hideCursor() {}, showCursor() {} };
const ui = new TuiMainScreen(terminal);
const record = label => ({ focused: false, inputs: [], render() { return [label]; }, invalidate() {}, handleInput(data) { this.inputs.push(data); } });
const editor = Object.assign(record('EDITOR'), { getText: () => '', setText() {} });
const editorContainer = new Container();
editorContainer.addChild(editor);
ui.addChild(editorContainer);
const palette = record('PALETTE');
ui.addChild(palette);
ui.setFocus(palette);
const mode = Object.assign(Object.create(InteractiveMode.prototype), { editor, editorContainer, keybindings: {}, ui, disposeActiveSelector() {} });
const mount = async (overlay, label) => {
 let close;
 const component = record(label);
 const done = mode.showExtensionCustom((_t, _th, _k, finish) => { close = finish; return component; }, { overlay });
 // The real method installs the resolved factory component in its Promise.then microtask.
 await Promise.resolve();
 return { component, close, done };
};
ui.start();
try {
 const overlay = await mount(true, 'OVERLAY');
 const overlayFocused = ui.getFocusedComponent() === overlay.component;
 const replacement = await mount(false, 'REPLACEMENT');
 const replacementFocused = ui.getFocusedComponent() === replacement.component;
 receive('r');
 assert.deepEqual(replacement.component.inputs, ['r']);
 replacement.close('done');
 assert.equal(await replacement.done, 'done');
 receive('x');
 assert.deepEqual(overlay.component.inputs, ['x']);
 assert.deepEqual(editor.inputs, []);
 const restoredFocus = ui.getFocusedComponent() === overlay.component;
 overlay.close('closed');
 assert.equal(await overlay.done, 'closed');
 const standalone = await mount(false, 'STANDALONE');
 standalone.close('standalone done');
 assert.equal(await standalone.done, 'standalone done');
 const editorRestored = ui.getFocusedComponent() === editor;
 console.log(JSON.stringify({ overlayFocused, replacementFocused, restoredFocus, editorRestored, editor: editor.getText() }));
} finally { ui.stop(); }
