import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import { dirname, resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
const packageRoot = resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const parent = pathToFileURL(join(packageRoot, 'package.json')).href;
const dist = dirname(fileURLToPath(import.meta.resolve('@earendil-works/pi-agent-core', parent)));
assert.equal(JSON.parse(readFileSync(join(dist, '..', 'package.json'), 'utf8')).version, '0.87.1');
const load = path => import(pathToFileURL(join(dist, 'harness', path + '.js')).href);
const { NodeExecutionEnv } = await load('env/nodejs');
const { BACKGROUND_CONTEXT: background, withAbortSignal } = await load('context');
const { getOrThrow, ok } = await load('types');
const { createReadTool } = await load('tools/read');
const { createWriteTool } = await load('tools/write');
const { createEditTool } = await load('tools/edit');
const { createBashTool } = await load('tools/bash');
const { truncateTail, DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES } = await load('utils/truncate');
const cwd = await mkdtemp(join(tmpdir(), 'pig-harness-tools-'));
const env = new NodeExecutionEnv({ cwd });
const invocation = { invocationId: 'test-result', operationId: 'test-operation', turnId: 'test-turn', getMemo: async () => undefined, setMemo: async () => {} };
const noUpdate = () => {};
const text = result => result.content.flatMap(block => block.type === 'text' ? [block.text] : []).join('\n');
const execute = (tool, input, currentEnv = env, update = noUpdate, context = background) => tool.execute('test', input, update, { env: currentEnv }, invocation, context);
const report = (name, value) => console.log('HARNESS_TOOLS ' + name + ' ' + JSON.stringify(value));
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
const output = (text, options) => {
  const truncated = truncateTail(text, options?.capture?.limits ?? { maxBytes: DEFAULT_MAX_BYTES, maxLines: DEFAULT_MAX_LINES });
  const { content, ...truncation } = truncated;
  options.onUpdate({ kind: 'replace', output: { text: content, truncation } }, background);
  return { exitCode: 0, truncation };
};
try {
  getOrThrow(await env.writeFile('test.txt', Array.from({ length: 100 }, (_, i) => `Line ${i + 1}`).join('\n'), background));
  report('read', text(await execute(createReadTool(), { path: 'test.txt', offset: 41, limit: 20 })));
  const write = await execute(createWriteTool(), { path: 'nested/edit.txt', content: 'alpha\nbeta\ngamma\ndelta\n' });
  const edit = await execute(createEditTool(), { path: 'nested/edit.txt', edits: [{ oldText: 'alpha\n', newText: 'ALPHA\n' }, { oldText: 'gamma\n', newText: 'GAMMA\n' }] });
  report('edit', [text(write), text(edit), edit.details.diff, edit.details.patch, getOrThrow(await env.readTextFile('nested/edit.txt', background))]);
  const bmp = new Uint8Array(58), view = new DataView(bmp.buffer);
  bmp[0] = 0x42; bmp[1] = 0x4d;
  for (const [offset, value] of [[2, 58], [10, 54], [14, 40], [18, 1], [22, 1], [34, 4]]) view.setUint32(offset, value, true);
  view.setUint16(26, 1, true); view.setUint16(28, 24, true);
  getOrThrow(await env.writeFile('image.bmp', bmp, background));
  let received;
  const image = await execute(createReadTool({ autoResizeImages: false, imageProcessor: async (bytes, mime, options) => { received = [mime, options.autoResizeImages, bytes.length]; return { ok: true, data: 'converted', mimeType: 'image/png', hints: ['[Image converted from image/bmp to image/png.]'] }; } }), { path: 'image.bmp' });
  report('image', [received, text(image), image.content[1].data, image.content[1].mimeType]);
  let late;
  class LateEnv extends NodeExecutionEnv { async exec(_command, options) { const result = output('before\n', options); late = () => output('before\nlate\n', options); return ok(result); } }
  const updates = [];
  const lateResult = await execute(createBashTool(), { command: 'late' }, new LateEnv({ cwd }), update => updates.push(text(update)));
  late(); report('late', [text(lateResult), updates]);
  // Only the checkpoint clock is virtual, as in upstream's vi.useFakeTimers.
  // Production imports remain unchanged; no filesystem or shell clock is faked.
  const realNow = Date.now;
  let now = 0;
  Date.now = () => now;
  try {
    class CheckpointEnv extends NodeExecutionEnv { async exec(_command, options) { output('one\n', options); now += 2100; output('one\ntwo\n', options); now += 100; output('one\ntwo\nthree\n', options); now += 2000; return ok(output('one\ntwo\nthree\nfour\n', options)); } }
    const checkpoints = [];
    await execute(createBashTool(), { command: 'controlled' }, new CheckpointEnv({ cwd }), (update, options) => { if (options?.checkpoint) checkpoints.push(text(update)); });
    report('checkpoints', checkpoints);
  } finally { Date.now = realNow; }
  for (const edit of [false, true]) {
    const started = deferred(), release = deferred();
    let secondStarted = false, firstSettled = false;
    class BlockingEnv extends NodeExecutionEnv { async writeFile(path, content, context) {
      if (content === 'first\n' || content === 'ALPHA\nbeta\n') { started.resolve(); await release.promise; if (edit) { const result = await super.writeFile(path, content, background); firstSettled = true; return result; } }
      if (['second\n', 'ALPHA\nBETA\n', 'alpha\nBETA\n'].includes(content)) secondStarted = true;
      return super.writeFile(path, content, context);
    } }
    const current = new BlockingEnv({ cwd });
    if (edit) getOrThrow(await current.writeFile('file.txt', 'alpha\nbeta\n', background));
    const tool = edit ? createEditTool() : createWriteTool();
    const controller = new AbortController();
    const firstInput = edit ? { path: 'file.txt', edits: [{ oldText: 'alpha', newText: 'ALPHA' }] } : { path: 'file.txt', content: 'first\n' };
    const secondInput = edit ? { path: 'file.txt', edits: [{ oldText: 'beta', newText: 'BETA' }] } : { path: 'file.txt', content: 'second\n' };
    const first = execute(tool, firstInput, current, noUpdate, withAbortSignal(controller.signal, background));
    await started.promise; controller.abort();
    const second = execute(tool, secondInput, current);
    await new Promise(resolve => setTimeout(resolve, 20));
    const secondBefore = secondStarted;
    release.resolve();
    await assert.rejects(first);
    await second;
    report(edit ? 'edit-queue' : 'write-queue', [edit, secondBefore, firstSettled, getOrThrow(await current.readTextFile('file.txt', background))]);
  }
} finally { await env.cleanup(background); await rm(cwd, { recursive: true, force: true }); }
