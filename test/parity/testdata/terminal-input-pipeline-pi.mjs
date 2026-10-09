// Pi 1.1.0 TuiBase.handleTerminalInput (tui.ts:1044-1120) driven through a real renderer's terminal input callback.
// Each line is one case: what the input listeners and the focused components saw.
import assert from 'node:assert/strict';
import { readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? realpathSync('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.1.0');
const dist = join(root, 'node_modules/@earendil-works/pi-tui/dist');
const { TuiMainScreen } = await import(pathToFileURL(join(dist, 'tui-main-screen.js')).href);
const DEBUG = '\x1b[100;6u';
const RELEASE = '\x1b[97;1:3u';
const record = (extra = {}) => ({ inputs: [], render() { return ['x']; }, invalidate() {}, handleInput(data) { this.inputs.push(data); }, ...extra });
function run(name, scenario) {
 let receive;
 const terminal = { columns: 80, rows: 24, start(input) { receive = input; }, stop() {}, write() {}, hideCursor() {}, showCursor() {} };
 const ui = new TuiMainScreen(terminal);
 ui.start();
 const log = [];
 try { scenario(ui, receive, log); } finally { ui.stop(); }
 process.stdout.write(JSON.stringify({ case: name, ...log[0] }) + '\n');
}
run('listeners-transform-then-consume', (ui, receive, out) => {
 const focus = record(); ui.setFocus(focus);
 const seen = [];
 ui.addInputListener(d => { seen.push('a:' + d); return d === 'c' ? { consume: true } : { data: d.toUpperCase() }; });
 ui.addInputListener(d => { seen.push('b:' + d); });
 for (const d of ['x', 'c']) receive(d);
 out.push({ listeners: seen, focus: focus.inputs });
});
run('listener-empty-replacement-drops-input', (ui, receive, out) => {
 const focus = record(); ui.setFocus(focus);
 ui.addInputListener(() => ({ data: '' }));
 receive('x');
 out.push({ focus: focus.inputs });
});
run('debug-key-needs-a-callback', (ui, receive, out) => {
 const focus = record(); ui.setFocus(focus);
 let calls = 0;
 receive(DEBUG);
 ui.onDebug = () => { calls++; };
 receive(DEBUG);
 out.push({ calls, focus: focus.inputs });
});
run('listener-replaces-the-debug-key', (ui, receive, out) => {
 const focus = record(); ui.setFocus(focus);
 let calls = 0;
 ui.onDebug = () => { calls++; };
 ui.addInputListener(() => ({ data: 'z' }));
 receive(DEBUG);
 out.push({ calls, focus: focus.inputs });
});
run('key-release-needs-wants-key-release', (ui, receive, out) => {
 const plain = record(); ui.setFocus(plain);
 receive(RELEASE);
 const aware = record({ wantsKeyRelease: true }); ui.setFocus(aware);
 receive(RELEASE);
 out.push({ plain: plain.inputs, aware: aware.inputs });
});
run('listeners-see-key-releases', (ui, receive, out) => {
 const focus = record(); ui.setFocus(focus);
 const seen = [];
 ui.addInputListener(d => { seen.push(d); });
 receive(RELEASE);
 out.push({ listeners: seen, focus: focus.inputs });
});
run('overlay-visibility-callback-redirects-input', (ui, receive, out) => {
 const base = record(), overlay = record(); ui.setFocus(base);
 let shown = true;
 ui.showOverlay(overlay, { visible: () => shown });
 receive('a');
 shown = false;
 receive('b');
 out.push({ overlay: overlay.inputs, base: base.inputs });
});
