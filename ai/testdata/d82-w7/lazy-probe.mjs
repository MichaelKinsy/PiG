// Microtask-order probe for Pi's lazyStream (packages/ai/src/api/lazy.ts) and EventStream (utils/event-stream.ts).
// It imports the pinned Pi source itself (Node type stripping) and prints, per scenario, the order in which
// user-visible steps ran. ai/lazy_prefix_probe_test.go replays every scenario on the Go executor and must match exactly.
// Usage: node lazy-probe.mjs <pi-packages-root> [out.json]
import { writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const root = process.argv[2];
const { lazyStream } = await import(pathToFileURL(root + '/ai/src/api/lazy.ts'));
const { AssistantMessageEventStream } = await import(pathToFileURL(root + '/ai/src/utils/event-stream.ts'));

const model = { api: 'probe-api', provider: 'probe', id: 'probe-model' };
const message = (stopReason = 'pending') => ({ role: 'assistant', content: [], api: 'probe-api', provider: 'probe', model: 'probe-model', usage: {}, stopReason, timestamp: 0 });
let log;
const mark = (label) => log.push(label);
const microtask = (label) => queueMicrotask(() => mark(label));
// A chain of n microtasks, each queued by the previous one, measures the exact reaction at which an event is observed.
const chain = (n, index = 0) => queueMicrotask(() => { mark('c' + index); if (index + 1 < n) chain(n, index + 1); });

// An inner stream whose events are pushed by a microtask chain that starts when the stream is created.
function pusher(steps) {
	const inner = new AssistantMessageEventStream();
	const partial = message();
	let index = 0;
	const step = () => {
		if (index === steps) {
			inner.push({ type: 'done', reason: 'stop', message: message('stop') });
			inner.end();
			mark('push:done');
			return;
		}
		const n = index++;
		inner.push({ type: 'text_delta', contentIndex: 0, delta: 'd' + n, partial });
		mark('push:' + n);
		queueMicrotask(step);
	};
	queueMicrotask(step);
	return inner;
}

async function consume(stream) {
	for await (const event of stream) {
		mark('event:' + event.type + (event.delta ? ':' + event.delta : event.type === 'error' ? ':' + event.error.errorMessage : ''));
	}
	mark('consumer-done');
	const result = await stream.result();
	mark('result:' + result.stopReason);
}

const scenarios = {
	// setup runs synchronously to its first await; nothing queued by the caller before or after can overtake the .then reaction.
	'sync-setup-order': async () => {
		mark('call');
		microtask('m1');
		const stream = lazyStream(model, async () => { mark('setup'); return pusher(2); });
		microtask('m2');
		queueMicrotask(() => queueMicrotask(() => mark('m3')));
		mark('returned');
		await consume(stream);
	},
	// The synchronous prefix of a setup that awaits still runs before lazyStream returns.
	'sync-prefix-then-await': async () => {
		mark('call');
		microtask('m1');
		const stream = lazyStream(model, async () => { mark('prefix'); await null; mark('resumed'); return pusher(2); });
		microtask('m2');
		mark('returned');
		await consume(stream);
	},
	'await-first-setup': async () => {
		mark('call');
		microtask('m1');
		const stream = lazyStream(model, async () => { await null; mark('setup'); return pusher(2); });
		microtask('m2');
		mark('returned');
		await consume(stream);
	},
	'sync-setup-error': async () => {
		mark('call');
		microtask('m1');
		const stream = lazyStream(model, async () => { mark('setup'); throw new Error('boom'); });
		microtask('m2');
		microtask('m3');
		chain(8);
		mark('returned');
		await consume(stream);
	},
	'await-first-setup-error': async () => {
		mark('call');
		microtask('m1');
		const stream = lazyStream(model, async () => { await null; mark('setup'); throw new Error('boom'); });
		microtask('m2');
		microtask('m3');
		microtask('m4');
		chain(8);
		mark('returned');
		await consume(stream);
	},
	// Model runtime (await first) -> provider composer (synchronous) -> lazyApi (await first): three forwarders.
	'model-runtime-composer-api': async () => {
		mark('call');
		const api = () => lazyStream(model, async () => { await null; mark('api-setup'); return pusher(3); });
		const composer = () => lazyStream(model, async () => { mark('composer-setup'); return api(); });
		const stream = lazyStream(model, async () => { await null; mark('runtime-setup'); return composer(); });
		microtask('m1');
		mark('returned');
		await consume(stream);
	},
	// The composer's synchronous setup can hand back a source whose events were pushed before forwarding starts.
	'sync-setup-prepushed-source': async () => {
		mark('call');
		const stream = lazyStream(model, async () => {
			mark('setup');
			const inner = new AssistantMessageEventStream();
			inner.push({ type: 'text_delta', contentIndex: 0, delta: 'early', partial: message() });
			mark('push:early');
			queueMicrotask(() => { inner.push({ type: 'done', reason: 'stop', message: message('stop') }); inner.end(); mark('push:done'); });
			return inner;
		});
		microtask('m1');
		microtask('m2');
		mark('returned');
		await consume(stream);
	},
};

const out = {};
for (const [name, run] of Object.entries(scenarios)) {
	log = [];
	await run();
	await new Promise((resolve) => setImmediate(resolve));
	out[name] = log;
}
const text = JSON.stringify(out, null, 2) + '\n';
if (process.argv[3]) await writeFile(process.argv[3], text);
else process.stdout.write(text);
