// Scoped-wait probe: what a stream consumer observes while an awaited listener waits on a promise reaction, a process.nextTick callback or an I/O (setImmediate) callback.
// The consumer is packages/agent/src/agent-loop.ts:408-417 (for await + await emit) over packages/agent/src/agent.ts:605-607 (processEvents awaits each listener in turn),
// and the provider is a microtask chain pushing into Pi's real EventStream (utils/event-stream.ts). Usage: node scoped-wait-probe.mjs <pi-packages-root> [out.json]
import { writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const root = process.argv[2];
const { AssistantMessageEventStream } = await import(pathToFileURL(root + '/ai/src/utils/event-stream.ts'));

const message = (stopReason = 'pending') => ({ role: 'assistant', content: [], api: 'probe-api', provider: 'probe', model: 'probe-model', usage: {}, stopReason, timestamp: 0 });
let log;
const mark = (label) => log.push(label);
const chain = (n, index = 0) => queueMicrotask(() => { mark('c' + index); if (index + 1 < n) chain(n, index + 1); });

// A provider whose steps are microtasks; `ioAt` makes step n arrive from an I/O callback (setImmediate) scheduled when the stream is created.
function provider(steps, ioAt) {
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
		if (n + 1 === ioAt) setImmediate(step);
		else queueMicrotask(step);
	};
	if (ioAt === 0) setImmediate(step);
	else queueMicrotask(step);
	return inner;
}

async function run(steps, ioAt, wait) {
	const stream = provider(steps, ioAt);
	chain(24);
	const listeners = [async (event) => {
		mark('listener:' + (event.delta ?? event.type));
		await wait();
		mark('listener-end:' + (event.delta ?? event.type));
	}];
	const emit = async (event) => { for (const listener of listeners) await listener(event); };
	for await (const event of stream) {
		mark('event:' + (event.delta ?? event.type));
		await emit(event);
		mark('emitted:' + (event.delta ?? event.type));
	}
	mark('consumer-done');
}

const waits = {
	micro: () => Promise.resolve(),
	tick: () => new Promise((resolve) => process.nextTick(resolve)),
	immediate: () => new Promise((resolve) => setImmediate(resolve)),
};

const scenarios = {};
for (const [name, wait] of Object.entries(waits)) {
	scenarios[`wait-${name}`] = () => run(3, -1, wait);
	// The provider's second step is delivered from an I/O callback that was scheduled before the listener waits.
	scenarios[`wait-${name}-io-provider`] = () => run(3, 1, wait);
}

const out = {};
for (const [name, scenario] of Object.entries(scenarios)) {
	log = [];
	await scenario();
	await new Promise((resolve) => setImmediate(resolve));
	await new Promise((resolve) => setImmediate(resolve));
	out[name] = log;
}
const text = JSON.stringify(out, null, 2) + '\n';
if (process.argv[3]) await writeFile(process.argv[3], text);
else process.stdout.write(text);
