#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import ts from '../interface-extractor/node_modules/typescript/lib/typescript.js';

const pin = readFileSync('coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/)[1];
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
if (JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version !== pin) throw new Error('wrong Pi oracle version');
const load = (path) => import(pathToFileURL(join(root, path)).href);
const dataModule = (source) => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
let source = ts.transpileModule(readFileSync('.upstream/current/packages/coding-agent/src/utils/tools-manager.ts', 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext },
}).outputText;
// Only dependencies are fake. The entire pinned ensureTool implementation runs unchanged.
const dependencies = {
  'fs': 'export * from "node:fs"; export const existsSync = () => false;',
  'child_process': 'export const spawnSync = () => ({ error: new Error("not found") });',
  '../config.ts': 'export const APP_NAME = "pi"; export const getBinDir = () => process.env.PI_CODING_AGENT_DIR;',
  './management-http.ts': 'export const fetchWithRetry = (...args) => globalThis.fetch(...args);',
};
for (const [specifier, stub] of Object.entries(dependencies)) {
  const needle = `from "${specifier}"`;
  if (source.split(needle).length !== 2) throw new Error('dependency drift: ' + specifier);
  source = source.replace(needle, `from "${dataModule(stub)}"`);
}
const { ensureTool } = await import(dataModule(source));
const cases = [
  ['plain', new Error('boom')],
  ['hidden cause', new Error('fetch failed', { cause: new Error('connect ETIMEDOUT') })],
  ['duplicate', new Error('fetch failed', { cause: new Error('fetch failed', { cause: new Error('TLS failure') }) })],
  ['wrapped Go error', new Error('fetch failed', { cause: new Error('DNS failure') })],
  ['wrapped hidden cause', new Error('download', { cause: new Error('fetch failed', { cause: new Error('DNS failure') }) })],
  ['depth cap', new Error('one', { cause: new Error('two', { cause: new Error('three', { cause: new Error('four', { cause: new Error('five', { cause: new Error('six') }) }) }) }) })],
];
const cycle = new Error('fetch failed');
cycle.cause = cycle;
cases.push(['cycle', cycle], ['empty', new Error('', { cause: new Error('detail') })]);
const callbacks = [];
process.env.PI_OFFLINE = '';
for (const [name, error] of cases) {
  globalThis.fetch = async () => { throw error; };
  const statuses = [];
  if (await ensureTool('fd', (status) => statuses.push(status)) !== undefined) throw new Error('unexpected tool');
  callbacks.push({ name, statuses });
}
process.env.PI_OFFLINE = '1';
for (const tool of ['fd', 'rg']) {
  const statuses = [];
  await ensureTool(tool, (status) => statuses.push(status));
  callbacks.push({ name: 'offline ' + tool, statuses });
}
console.log('MANAGED_TOOL_CALLBACKS ' + JSON.stringify(callbacks));

const { InteractiveMode } = await load('dist/modes/interactive/interactive-mode.js');
const { initTheme } = await load('dist/modes/interactive/theme/theme.js');
const { Container } = await load('node_modules/@earendil-works/pi-tui/dist/index.js');
const renders = [];
for (const theme of ['dark', 'light']) {
  initTheme(theme, false);
  for (const width of [24, 80, 120]) {
    const m = Object.assign(Object.create(InteractiveMode.prototype), {
      chatContainer: new Container(), ui: { requestRender() {} },
    });
    m.showManagedToolStatus({ type: 'info', message: 'fd not found. Downloading...' });
    m.showManagedToolStatus({ type: 'warning', message: 'Failed to download fd: fetch failed: connect ETIMEDOUT' });
    renders.push({ theme, width, rows: m.chatContainer.render(width) });
  }
}
console.log('MANAGED_TOOL_RENDER ' + JSON.stringify(renders));
