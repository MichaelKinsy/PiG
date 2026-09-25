#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const pin = readFileSync('coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/)[1];
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
if (JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version !== pin) throw new Error('wrong Pi oracle version');
const load = (path) => import(pathToFileURL(join(root, path)).href);
const { InteractiveMode } = await load('dist/modes/interactive/interactive-mode.js');
const { IdleStatus } = await load('dist/modes/interactive/components/status-indicator.js');
const { initTheme } = await load('dist/modes/interactive/theme/theme.js');
const { Container, setKeybindings } = await load('node_modules/@earendil-works/pi-tui/dist/index.js');
const { KeybindingsManager } = await load('dist/core/keybindings.js');
setKeybindings(new KeybindingsManager());
initTheme('dark', false);

// Run real Pi countdown callbacks against a controlled clock, without sleeping
// or changing the indicator's message ourselves. Spinner frames are not sampled.
const intervals = new Map();
let nextID = 1;
const originalSetInterval = globalThis.setInterval;
const originalClearInterval = globalThis.clearInterval;
globalThis.setInterval = (callback, delay) => {
  const id = nextID++;
  intervals.set(id, { callback, delay });
  return id;
};
globalThis.clearInterval = (id) => intervals.delete(id);

const traces = [];
try {
  for (const mode of ['regular', 'fullscreen']) {
    for (const embedded of [false, true]) {
      for (const clearOnShrink of [false, true]) {
        const editor = {
          embedWorkingStatus: embedded,
          setWorkingStatusIndicator(indicator) { this.indicator = indicator; },
          onEscape() {},
        };
        const m = Object.assign(Object.create(InteractiveMode.prototype), {
          isInitialized: true,
          footer: { invalidate() {} },
          runtimeHost: { session: { settingsManager: { getShowTerminalProgress: () => false } } },
          options: { tuiMode: mode },
          ui: { requestRender() {}, getClearOnShrink: () => clearOnShrink },
          chatContainer: new Container(),
          statusContainer: new Container(),
          defaultEditor: editor,
          editor,
          idleStatus: new IdleStatus(),
        });
        const phases = [];
        const sample = () => phases.push({
          kind: m.activeStatusIndicator?.kind ?? '',
          message: m.activeStatusIndicator?.message ?? '',
          embedded: m.activeWorkingIndicatorEmbedded ?? false,
          standaloneRows: m.statusContainer.render(120).length,
        });
        await m.handleEvent({ type: 'compaction_start', reason: 'manual' });
        sample();
        await m.handleEvent({ type: 'summarization_retry_scheduled', attempt: 1, maxAttempts: 3, delayMs: 2100, errorMessage: 'overloaded' });
        sample();
        for (const { callback, delay } of [...intervals.values()]) if (delay === 1000) callback();
        sample();
        await m.handleEvent({ type: 'summarization_retry_attempt_start', source: 'branchSummary' });
        sample();
        await m.handleEvent({ type: 'summarization_retry_finished' });
        sample();
        m.clearStatusIndicator('branchSummary');
        sample();
        await m.handleEvent({ type: 'summarization_retry_scheduled', attempt: 2, maxAttempts: 3, delayMs: 4000, errorMessage: 'overloaded again' });
        sample();
        await m.handleEvent({ type: 'summarization_retry_finished' });
        sample();
        if (intervals.size !== 0) throw new Error('disposed status retained a timer');
        traces.push({ mode, embedded, clearOnShrink, phases });
      }
    }
  }
  console.log('STATUS_LIFECYCLE ' + JSON.stringify(traces));
} finally {
  globalThis.setInterval = originalSetInterval;
  globalThis.clearInterval = originalClearInterval;
}
