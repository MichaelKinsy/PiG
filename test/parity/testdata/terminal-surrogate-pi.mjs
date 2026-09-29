import assert from 'node:assert/strict';
const root = new URL(`file://${process.cwd()}/`);
const { Editor, theme, testTUI } = await import(new URL('test/parity/testdata/editor-wrap-helper/pi-runtime.mjs', root));
const { StdinBuffer } = await import(new URL('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/stdin-buffer.js', root));
const units = s => Array.from({length: s.length}, (_, i) => s.charCodeAt(i));
const e = new Editor(testTUI(), theme);
const input = new StdinBuffer();
const observed = [];
input.on('data', data => {
  observed.push({units: units(data), editor: units(e.getText())});
  e.handleInput(data);
});
try {
  input.process(Buffer.from('A😀B'));
  assert.deepEqual(observed, [
    {units: [65], editor: []},
    {units: [0xd83d], editor: [65]},
    {units: [0xde00], editor: [65, 0xd83d]},
    {units: [66], editor: [65, 0xd83d, 0xde00]},
  ]);
  console.log(JSON.stringify({observed, text: units(e.getText()), rewrites: ['\ud83d', '\ude00'].map(units)}));
} finally { input.destroy(); }
