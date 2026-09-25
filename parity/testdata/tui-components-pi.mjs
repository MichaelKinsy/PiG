#!/usr/bin/env node
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

function version() {
  const m = readFileSync('coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/);
  if (!m) throw new Error('cannot read UpstreamVersion');
  return m[1];
}
const root = process.env.PI_PACKAGE_ROOT ?? join(homedir(), '.local/share/mise/installs/npm-earendil-works-pi-coding-agent', version(), 'lib/node_modules/@earendil-works/pi-coding-agent');
const tuiRoot = join(root, 'node_modules/@earendil-works/pi-tui/dist');
const codingComponents = join(root, 'dist/modes/interactive/components');
const { Loader } = await import(pathToFileURL(join(tuiRoot, 'index.js')).href);
const { initTheme } = await import(pathToFileURL(join(root, 'dist/modes/interactive/theme/theme.js')).href);
const { BorderedLoader } = await import(pathToFileURL(join(codingComponents, 'bordered-loader.js')).href);
const { CountdownTimer } = await import(pathToFileURL(join(codingComponents, 'countdown-timer.js')).href);
const { visibleWidth } = await import(pathToFileURL(join(tuiRoot, 'utils.js')).href);
initTheme('dark', false);

const out = {
  loader: probeLoader(),
  bordered: probeBorderedLoader(),
  countdown: await probeCountdownTimer(),
  componentOK: true,
};
console.log(stableStringify(out));

function probeLoader() {
  const ui = fakeTUI();
  const loader = new Loader(ui, (s) => s, (s) => s, 'Loading...');
  const lines = loader.render(40);
  const before = lines.join('\n');
  loader.currentFrame = (loader.currentFrame + 1) % loader.frames.length;
  loader.updateDisplay();
  const after = loader.render(40).join('\n');
  loader.stop();
  return {
    lineCount: lines.length,
    firstEmpty: lines[0] === '',
    secondTrimmed: stripANSI(lines[1]).trim(),
    secondWidth: visibleWidth(lines[1]),
    frameAdvanced: before !== after,
    emptyFramePrefix: emptyFramePrefix(),
  };
}

function emptyFramePrefix() {
  const loader = new Loader(fakeTUI(), (s) => s, (s) => s, 'waiting', { frames: [] });
  const lines = loader.render(30);
  loader.stop();
  return stripANSI(lines[1]).trim();
}

function probeBorderedLoader() {
  const ui = fakeTUI();
  const theme = { fg: (_name, s) => s };
  const cancellable = new BorderedLoader(ui, theme, 'Loading data...', { cancellable: true });
  const cancellableLines = cancellable.render(60);
  const non = new BorderedLoader(ui, theme, 'Processing...', { cancellable: false });
  const nonLines = non.render(60);
  const before = cancellableLines.join('\n');
  cancellable.loader.currentFrame = (cancellable.loader.currentFrame + 1) % cancellable.loader.frames.length;
  cancellable.loader.updateDisplay();
  const after = cancellable.render(60).join('\n');
  cancellable.dispose();
  non.dispose();
  return {
    cancellableLines: cancellableLines.length,
    cancellableContains: containsAll(stripANSI(cancellableLines.join('\n')), 'Loading data', 'cancel'),
    nonCancellableLines: nonLines.length,
    nonContains: containsAll(stripANSI(nonLines.join('\n')), 'Processing'),
    nonHasCancel: stripANSI(nonLines.join('\n')).includes('cancel'),
    hasBorders: stripANSI(cancellableLines[0]).includes('─') && stripANSI(cancellableLines[cancellableLines.length - 1]).includes('─'),
    frameAdvanced: before !== after,
  };
}

async function probeCountdownTimer() {
  const ticks = [];
  let expired = false;
  const timer = new CountdownTimer(1100, fakeTUI(), (seconds) => ticks.push(seconds), () => { expired = true; });
  await new Promise((resolve) => setTimeout(resolve, 1250));
  timer.dispose();
  return { ticks, expired };
}

function fakeTUI() {
  return { requestRender() {} };
}

function containsAll(s, ...parts) {
  return parts.every((part) => s.includes(part));
}

function stripANSI(s) {
  return String(s ?? '').replace(/\x1b\[[0-9;]*m/g, '');
}

function stableStringify(value) {
  return JSON.stringify(sortValue(value), null, 2);
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === 'object') {
    const out = {};
    for (const key of Object.keys(value).sort()) out[key] = sortValue(value[key]);
    return out;
  }
  return value;
}
