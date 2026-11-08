#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

// Drives the pinned Pi InteractiveMode.handleEvent against stubbed collaborators and records, after every step of each corpus sequence, what a user can
// observe: the terminal progress writes, the status indicator, where Escape goes, the pending-messages display, the editor border, the terminal title,
// whether shutdown was requested and the chat text. The Go probe TestHandleEventProbe runs the same corpus through the Pig owner loop.
// The border color is compared as truecolor SGR on both sides.
process.env.COLORTERM = 'truecolor';
const pin = readFileSync('internal/coding/pigversion/pigversion.go', 'utf8').match(/UpstreamPackageVersion = "([^"]+)"/)[1];
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
if (JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version !== pin) throw new Error('wrong Pi oracle version');
const load = (path) => import(pathToFileURL(join(root, path)).href);
const { InteractiveMode } = await load('dist/modes/interactive/interactive-mode.js');
const { IdleStatus } = await load('dist/modes/interactive/components/status-indicator.js');
const themeModule = await load('dist/modes/interactive/theme/theme.js');
const { Container, setKeybindings } = await load('node_modules/@earendil-works/pi-tui/dist/index.js');
const { KeybindingsManager } = await load('dist/core/keybindings.js');
const corpus = JSON.parse(readFileSync('test/parity/testdata/handle-event-corpus.json', 'utf8'));
setKeybindings(new KeybindingsManager());
themeModule.initTheme('dark', false);
const { theme } = themeModule;

const ANSI = /\u001b\[[0-9;?]*[A-Za-z]|\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)/g;
const text = (container) => container.render(100).map((line) => line.replace(ANSI, '').trimEnd()).join('\n').replace(/^\n+|\n+$/g, '');

const traces = [];
for (const sequence of corpus.sequences) {
  for (const showProgress of [true, false]) {
    const progress = [];
    const titles = [];
    const calls = { shutdown: 0, target: '' };
    const queue = { steering: [], followUp: [] };
    let sessionName;
    const session = {
      thinkingLevel: 'off',
      isIdle: true,
      settingsManager: { getShowTerminalProgress: () => showProgress, getShowCacheMissNotices: () => false, getShowImages: () => false, getImageWidthCells: () => 60 },
      abortRetry() { calls.target = 'retry'; },
      abortCompaction() { calls.target = 'compaction'; },
      getSteeringMessages: () => queue.steering,
      getFollowUpMessages: () => queue.followUp,
      sessionManager: { getSessionName: () => sessionName, getCwd: () => '/work/project' },
      getToolDefinition: () => undefined,
      extensionRunner: {
        resolveToolRenderers: (name, fallback) => fallback(),
        getMessageRenderer: () => undefined,
        getEntryRenderer: (type) => (type === 'note'
          ? (entry, options) => ({ render: () => [`entry:${entry.data}:expanded=${options.expanded}`], invalidate() {} })
          : undefined),
      },
    };
    const editor = {
      embedWorkingStatus: sequence.embedded === true,
      setWorkingStatusIndicator(indicator) { this.indicator = indicator; },
      borderColor: theme.getThinkingBorderColor('off'),
      getText: () => '',
    };
    const defaultEditor = Object.assign(editor, { onEscape() { calls.target = 'main'; } });
    const m = Object.assign(Object.create(InteractiveMode.prototype), {
      isInitialized: true,
      programStatus: { handleEvent() {} },
      footer: { invalidate() {} },
      runtimeHost: { session },
      options: { tuiMode: 'regular' },
      ui: { requestRender() {}, getClearOnShrink: () => false, terminal: { setProgress: (active) => progress.push(active), setTitle: (title) => titles.push(title) } },
      chatContainer: new Container(),
      statusContainer: new Container(),
      pendingMessagesContainer: new Container(),
      defaultEditor,
      editor,
      idleStatus: new IdleStatus(),
      workingVisible: true,
      defaultWorkingMessage: 'Working',
      compactionQueuedMessages: [],
      pendingTools: new Map(),
      pendingBashComponents: [],
      entriesRenderedByBoundaryCompaction: new Set(),
      toolOutputExpanded: sequence.expanded === true,
      outputPad: 1,
      flushCompactionQueue: async () => {},
      shutdown: async () => { calls.shutdown++; },
      hideThinkingBlock: false,
      hiddenThinkingLabel: 'Thinking...',
      getMarkdownTransformers: () => [],
      getMarkdownThemeWithSettings: () => themeModule.getMarkdownTheme(),
    });
    const steps = [];
    let previousChat = '';
    for (const step of sequence.steps) {
      progress.length = 0;
      titles.length = 0;
      calls.shutdown = 0;
      calls.target = '';
      const record = { step: step.name };
      if (step.event) {
        if (step.event.type === 'session_info_changed') sessionName = step.event.name;
        await m.handleEvent(step.event);
      } else if (step.op === 'esc') {
        m.defaultEditor.onEscape();
        record.esc = calls.target;
      } else if (step.op === 'queue') {
        queue.steering = step.steering ?? [];
        queue.followUp = step.followUp ?? [];
      } else if (step.op === 'thinking') {
        session.thinkingLevel = step.level;
      } else if (step.op === 'shutdownRequested') {
        m.shutdownRequested = true;
      } else if (step.op === 'workingVisible') {
        m.workingVisible = step.visible;
      } else {
        throw new Error('unknown step ' + JSON.stringify(step));
      }
      record.progress = [...progress];
      record.status = m.activeStatusIndicator ? `${m.activeStatusIndicator.kind}|${m.activeStatusIndicator.message ?? ''}` : '';
      record.pending = text(m.pendingMessagesContainer);
      record.border = m.editor.borderColor('x');
      record.titles = titles.map((title) => title.replace(/^[^ ]+ - /, ''));
      record.shutdown = calls.shutdown > 0;
      const chat = text(m.chatContainer);
      record.chat = chat === previousChat ? '=' : chat;
      previousChat = chat;
      steps.push(record);
    }
    traces.push({ name: sequence.name, showProgress, steps });
  }
}
console.log('HANDLE_EVENT ' + JSON.stringify(traces));
process.exit(0);
