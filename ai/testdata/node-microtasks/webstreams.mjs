// Prints the microtask/macrotask order of Node's Web Streams reader and values() iterator.
// Pinned to Node 24.19.0 internal/webstreams/readablestream.js. The Go replay in
// ai/node_web_stream_test.go must produce exactly the log this probe prints.
//
// Each scenario logs labels in execution order. A ticker chain (one microtask per
// step) makes reaction depth visible: an event logged between "tN" and "tN+1"
// ran N microtask generations after the ticker started. External completions
// (setImmediate) model socket data: they run only after the microtask queue drains.
import { pathToFileURL } from 'node:url';

export const scenarios = {};

function ticker(log, prefix, count) {
  const step = (n) => {
    if (n > count) return;
    Promise.resolve().then(() => {
      log.push(`${prefix}${n}`);
      step(n + 1);
    });
  };
  step(1);
}

const bytes = (...values) => new Uint8Array(values);
const show = (result) => (result.done ? 'done' : `[${Array.from(result.value).join(',')}]`);

// A push-style byte stream. controller.enqueue/close/error are the external events.
function makeStream(kind, controllerRef, cancel) {
  const source = {
    start(controller) { controllerRef.controller = controller; },
  };
  if (cancel) source.cancel = cancel;
  if (kind === 'bytes') source.type = 'bytes';
  return new ReadableStream(source, kind === 'default' ? { highWaterMark: 0 } : undefined);
}

function external(log, label, action) {
  setImmediate(() => {
    log.push(`external ${label}`);
    action();
    ticker(log, `${label}.t`, 6);
  });
}

const kinds = ['bytes', 'default'];

for (const kind of kinds) {
  // Buffered chunk, then pending chunk, then pending close.
  scenarios[`read-buffered-then-pending/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    ref.controller.enqueue(bytes(1, 2));
    const reader = stream.getReader();
    ticker(log, 't', 8);
    const first = reader.read();
    log.push('read1 called');
    log.push(`read1 ${show(await first)}`);
    const second = reader.read();
    log.push('read2 called');
    external(log, 'enqueue', () => ref.controller.enqueue(bytes(3)));
    log.push(`read2 ${show(await second)}`);
    const third = reader.read();
    log.push('read3 called');
    external(log, 'close', () => ref.controller.close());
    log.push(`read3 ${show(await third)}`);
    log.push(`read4 ${show(await reader.read())}`);
  };

  // A chunk followed by close() while the chunk is still queued.
  scenarios[`read-buffered-close-requested/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    ref.controller.enqueue(bytes(1));
    ref.controller.close();
    const reader = stream.getReader();
    ticker(log, 't', 8);
    log.push(`read1 ${show(await reader.read())}`);
    log.push(`read2 ${show(await reader.read())}`);
  };

  scenarios[`read-error/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    const reader = stream.getReader();
    ticker(log, 't', 8);
    const pending = reader.read();
    external(log, 'error', () => ref.controller.error(new Error('boom')));
    try {
      await pending;
      log.push('read resolved');
    } catch (error) {
      log.push(`read rejected ${error.message}`);
    }
    try {
      await reader.read();
      log.push('read2 resolved');
    } catch (error) {
      log.push(`read2 rejected ${error.message}`);
    }
  };

  scenarios[`reader-cancel-pending-read/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref, () => {});
    const reader = stream.getReader();
    ticker(log, 't', 8);
    const pending = reader.read();
    const cancelled = reader.cancel('stop');
    pending.then((result) => log.push(`pending read ${show(result)}`));
    cancelled.then(() => log.push('cancel settled'));
    log.push(`await pending ${show(await pending)}`);
    await cancelled;
    log.push('awaited cancel');
  };

  // values(): first next() is delayed by PromiseResolve().then(nextSteps).
  scenarios[`values-buffered/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    ref.controller.enqueue(bytes(1));
    ref.controller.enqueue(bytes(2));
    const iterator = stream.values();
    ticker(log, 't', 12);
    log.push(`next1 ${show(await iterator.next())}`);
    log.push(`next2 ${show(await iterator.next())}`);
    const third = iterator.next();
    log.push('next3 called');
    external(log, 'enqueue', () => ref.controller.enqueue(bytes(3)));
    log.push(`next3 ${show(await third)}`);
    const fourth = iterator.next();
    log.push('next4 called');
    external(log, 'close', () => ref.controller.close());
    log.push(`next4 ${show(await fourth)}`);
    log.push(`next5 ${show(await iterator.next())}`);
  };

  scenarios[`values-pending-first/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    const iterator = stream.values();
    ticker(log, 't', 12);
    const first = iterator.next();
    log.push('next1 called');
    external(log, 'enqueue', () => ref.controller.enqueue(bytes(9)));
    log.push(`next1 ${show(await first)}`);
    const second = iterator.next();
    log.push('next2 called');
    external(log, 'close', () => ref.controller.close());
    log.push(`next2 ${show(await second)}`);
  };

  scenarios[`values-close-requested/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    ref.controller.enqueue(bytes(1));
    ref.controller.close();
    const iterator = stream.values();
    ticker(log, 't', 12);
    log.push(`next1 ${show(await iterator.next())}`);
    log.push(`next2 ${show(await iterator.next())}`);
    log.push(`next3 ${show(await iterator.next())}`);
  };

  scenarios[`values-error/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    const iterator = stream.values();
    ticker(log, 't', 12);
    const pending = iterator.next();
    external(log, 'error', () => ref.controller.error(new Error('boom')));
    try {
      await pending;
      log.push('next resolved');
    } catch (error) {
      log.push(`next rejected ${error.message}`);
    }
    log.push(`next2 ${show(await iterator.next())}`);
  };

  // Concurrent next() calls chain through state.current.
  scenarios[`values-concurrent-next/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref);
    ref.controller.enqueue(bytes(1));
    ref.controller.enqueue(bytes(2));
    const iterator = stream.values();
    ticker(log, 't', 14);
    const a = iterator.next();
    const b = iterator.next();
    a.then((r) => log.push(`a ${show(r)}`));
    b.then((r) => log.push(`b ${show(r)}`));
    await b;
    log.push('awaited b');
  };

  for (const variant of ['none', 'sync', 'async', 'await-null']) {
    const cancelFor = () => {
      switch (variant) {
        case 'none': return undefined;
        case 'sync': return () => {};
        case 'async': return async () => {};
        case 'await-null': return async () => { await null; };
      }
    };
    // return() after one chunk: the for-await break path (readableStreamCancel then await).
    scenarios[`values-return/${kind}/${variant}`] = async (log) => {
      const ref = {};
      const stream = makeStream(kind, ref, cancelFor());
      ref.controller.enqueue(bytes(1));
      ref.controller.enqueue(bytes(2));
      const iterator = stream.values();
      ticker(log, 't', 14);
      log.push(`next1 ${show(await iterator.next())}`);
      log.push('return called');
      const returned = iterator.return();
      returned.then(() => log.push('return settled'));
      await returned;
      log.push('awaited return');
      log.push(`next2 ${show(await iterator.next())}`);
    };
  }

  // return() before any next(): started flips, state.current stays undefined.
  scenarios[`values-return-first/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref, () => {});
    const iterator = stream.values();
    ticker(log, 't', 10);
    const returned = iterator.return();
    returned.then(() => log.push('return settled'));
    await returned;
    log.push('awaited return');
  };

  // return() on an already-closed stream resolves through PromiseResolve().
  scenarios[`values-return-after-close/${kind}`] = async (log) => {
    const ref = {};
    const stream = makeStream(kind, ref, () => {});
    ref.controller.enqueue(bytes(1));
    ref.controller.close();
    const iterator = stream.values();
    ticker(log, 't', 14);
    log.push(`next1 ${show(await iterator.next())}`);
    log.push(`next2 ${show(await iterator.next())}`);
    const returned = iterator.return();
    returned.then(() => log.push('return settled'));
    await returned;
    log.push('awaited return');
  };
}

export async function run() {
  const results = {};
  for (const [name, scenario] of Object.entries(scenarios)) {
    const log = [];
    const guard = setTimeout(() => {
      console.error(`scenario ${name} never settled after: ${log.slice(-3).join(' | ')}`);
      process.exit(1);
    }, 2000);
    await scenario(log);
    clearTimeout(guard);
    // Let queued reactions and externals finish deterministically before reading the log.
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setImmediate(resolve));
    results[name] = log;
  }
  return results;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.stdout.write(`${JSON.stringify({ node: process.version, scenarios: await run() }, null, 1)}\n`);
}
