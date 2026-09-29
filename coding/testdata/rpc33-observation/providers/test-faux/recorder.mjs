// Loaded into the real `pi --mode rpc` process next to test/parity/testdata/test-faux-provider.ts (the Pi-side fixture that
// PiG's ai/test_faux.go pairs with). It registers that fixture's provider unchanged and records the single-thread
// interleaving of its `stream.push` calls with RPC serialization of assistant records. The recorder is synchronous
// (appendFileSync), so it adds no microtask or timer.
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const log = process.env.TESTFAUX_PROBE_LOG;
let pushes = 0;
const record = (entry) => appendFileSync(log, JSON.stringify(entry) + '\n');

// rpc-mode.ts:356 `output(toJsonEvent(event))` serializes one JSON line per session event.
const stringify = JSON.stringify;
JSON.stringify = function (value, ...rest) {
  const text = stringify.call(this, value, ...rest);
  const assistant = value && typeof value === 'object' && (
    ((value.type === 'message_start' || value.type === 'message_end') && value.message?.role === 'assistant') ||
    (value.type === 'message_update' && value.assistantMessageEvent !== undefined && value.usage !== undefined));
  if (assistant) record({ kind: 'serialize', pushed: pushes, record: JSON.parse(text) });
  return text;
};

export function wrapStream(streamSimple, counter) {
  return (model, context, options) => {
    const inner = streamSimple(model, context, options);
    const push = inner.push.bind(inner);
    inner.push = (event) => {
      counter.pushed++;
      return push(event);
    };
    return inner;
  };
}

const fixture = await import(pathToFileURL(process.env.TESTFAUX_PROVIDER).href);
export default function (pi) {
  const counter = { get pushed() { return pushes; }, set pushed(value) { pushes = value; } };
  fixture.default({
    registerProvider(name, config) {
      pi.registerProvider(name, { ...config, streamSimple: (model, context, options) => wrapStream(config.streamSimple, counter)(model, context, options) });
    },
  });
}
