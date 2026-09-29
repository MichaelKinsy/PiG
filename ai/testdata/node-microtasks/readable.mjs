// Prints the microtask, process.nextTick and macrotask order of a Node Readable's
// Symbol.asyncIterator (createAsyncIterator in lib/internal/streams/readable.js).
// Pinned to Node 24.19.0. The Go replay in ai/node_readable_test.go must produce
// exactly the log this probe prints.
//
// "plain" is a byte-mode Readable. "incoming" mimics http.IncomingMessage, whose
// readingMore starts set and whose _read clears it once (lib/_http_incoming.js).
// The "real-incoming" scenario reads an actual IncomingMessage whose whole
// response arrived in one socket read, and asserts that state before it reads.
import http from 'node:http';
import { Readable } from 'node:stream';
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

class Incoming extends Readable {
  constructor(log) {
    super();
    this._readableState.readingMore = true;
    this._consuming = false;
    this.log = log;
  }
  _read() {
    this.log.push('_read');
    if (!this._consuming) {
      this._readableState.readingMore = false;
      this._consuming = true;
    }
  }
}

// "manual" is a plain Readable with autoDestroy: false, so eos completes on 'end' instead of waiting for 'close'.
function makeReadable(flavor, log) {
  if (flavor === 'incoming') return new Incoming(log);
  return new Readable({ autoDestroy: flavor !== 'manual', read() { log.push('_read'); } });
}

const show = (result) => (result.done ? 'done' : `[${Array.from(result.value).join(',')}]`);
const buf = (...values) => Buffer.from(values);

function external(log, label, action) {
  setImmediate(() => {
    log.push(`external ${label}`);
    action();
    ticker(log, `${label}.t`, 6);
  });
}

async function step(log, label, iterator) {
  try {
    log.push(`${label} ${show(await iterator.next())}`);
  } catch (error) {
    log.push(`${label} rejected ${error.message}`);
  }
}

for (const flavor of ['plain', 'incoming', 'manual']) {
  // The transport already holds the whole body: chunk and EOF are buffered before the consumer starts.
  scenarios[`buffered-ended/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1, 2));
    readable.push(null);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    await step(log, 'next1', iterator);
    await step(log, 'next2', iterator);
    await step(log, 'next3', iterator);
    // The finally block removes the readable listener and the end-of-stream watcher, or destroys the stream.
    log.push(`listeners readable=${readable.listenerCount('readable')} end=${readable.listenerCount('end')} close=${readable.listenerCount('close')} destroyed=${readable.destroyed}`);
  };

  scenarios[`buffered-two-chunks-ended/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    readable.push(buf(2, 3));
    readable.push(null);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    await step(log, 'next1', iterator);
    await step(log, 'next2', iterator);
  };

  scenarios[`buffered-open-then-end/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    await step(log, 'next1', iterator);
    const second = iterator.next();
    log.push('next2 called');
    external(log, 'end', () => readable.push(null));
    log.push(`next2 ${show(await second)}`);
    await step(log, 'next3', iterator);
  };

  scenarios[`pending-data-end/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const first = iterator.next();
    log.push('next1 called');
    external(log, 'data', () => readable.push(buf(7)));
    log.push(`next1 ${show(await first)}`);
    const second = iterator.next();
    log.push('next2 called');
    external(log, 'end', () => readable.push(null));
    log.push(`next2 ${show(await second)}`);
  };

  scenarios[`pending-two-chunks/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const first = iterator.next();
    log.push('next1 called');
    external(log, 'data1', () => readable.push(buf(1)));
    log.push(`next1 ${show(await first)}`);
    const second = iterator.next();
    log.push('next2 called');
    external(log, 'data2', () => readable.push(buf(2)));
    log.push(`next2 ${show(await second)}`);
  };

  scenarios[`pending-coalesced-pushes/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const first = iterator.next();
    log.push('next1 called');
    external(log, 'burst', () => {
      readable.push(buf(1));
      readable.push(buf(2));
      readable.push(null);
    });
    log.push(`next1 ${show(await first)}`);
    await step(log, 'next2', iterator);
  };

  scenarios[`concurrent-next/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 14);
    const a = iterator.next();
    const b = iterator.next();
    a.then((r) => log.push(`a ${show(r)}`));
    b.then((r) => log.push(`b ${show(r)}`));
    log.push('called');
    external(log, 'data', () => readable.push(buf(2)));
    await b;
    log.push('awaited b');
  };

  scenarios[`return-after-first/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 14);
    await step(log, 'next1', iterator);
    log.push('return called');
    const returned = iterator.return();
    returned.then(() => log.push('return settled'));
    await returned;
    log.push('awaited return');
    await step(log, 'next2', iterator);
    log.push(`destroyed ${readable.destroyed}`);
  };

  scenarios[`return-before-next/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const returned = iterator.return();
    returned.then(() => log.push('return settled'));
    await returned;
    log.push('awaited return');
    log.push(`destroyed ${readable.destroyed}`);
  };

  scenarios[`return-while-pending/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const pending = iterator.next();
    const returned = iterator.return();
    pending.then((r) => log.push(`pending ${show(r)}`));
    returned.then((r) => log.push(`return settled ${show(r)}`));
    log.push('called');
    external(log, 'data', () => readable.push(buf(5)));
    await returned;
    log.push('awaited return');
    log.push(`destroyed ${readable.destroyed}`);
  };

  scenarios[`error-while-pending/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    const pending = iterator.next();
    log.push('next1 called');
    external(log, 'error', () => readable.destroy(new Error('boom')));
    try {
      log.push(`next1 ${show(await pending)}`);
    } catch (error) {
      log.push(`next1 rejected ${error.message}`);
    }
    await step(log, 'next2', iterator);
  };

  scenarios[`destroyed-before-next/${flavor}`] = async (log) => {
    const readable = makeReadable(flavor, log);
    readable.push(buf(1));
    readable.destroy();
    const iterator = readable[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    await step(log, 'next1', iterator);
    await step(log, 'next2', iterator);
  };
}

// A real http.IncomingMessage whose complete response arrived in one socket read.
scenarios['real-incoming/buffered-ended'] = async (log) => {
  const server = http.createServer((request, response) => {
    response.writeHead(200, { 'content-length': '2' });
    response.end(Buffer.from([1, 2]));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address();
  try {
    const response = await new Promise((resolve, reject) => {
      http.get({ host: '127.0.0.1', port, agent: false }, resolve).on('error', reject);
    });
    const state = response._readableState;
    if (!(state.length === 2 && state.ended && !state.endEmitted)) {
      throw new Error(`response did not arrive buffered and ended: length=${state.length} ended=${state.ended}`);
    }
    const iterator = response[Symbol.asyncIterator]();
    ticker(log, 't', 10);
    await step(log, 'next1', iterator);
    await step(log, 'next2', iterator);
    await step(log, 'next3', iterator);
  } finally {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
};

export async function run() {
  const results = {};
  for (const [name, scenario] of Object.entries(scenarios)) {
    const log = [];
    const guard = setTimeout(() => {
      console.error(`scenario ${name} never settled after: ${log.slice(-3).join(' | ')}`);
      process.exit(1);
    }, 4000);
    await scenario(log);
    clearTimeout(guard);
    // Let queued ticks, reactions and externals finish before reading the log.
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setImmediate(resolve));
    results[name] = log;
  }
  return results;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.stdout.write(`${JSON.stringify({ node: process.version, scenarios: await run() }, null, 1)}\n`);
}
