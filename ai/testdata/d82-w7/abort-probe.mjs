// Cancellation-text probe: runs a Pi-compatible CLI in --mode rpc against a server that holds the response at one point, sends {"type":"abort"}, and prints the assistant message that ends the turn.
// Phases: pre-headers (request received, nothing sent), post-headers (200 sent, no body), mid-body (200 plus one text record sent).
// The abort is sent when the point is observed, never after a delay: before headers, when the server has read the whole request; after headers, when the CLI's own event stream shows it handled what the server sent (the assistant message starts once the headers are in, and the first record ends in a text delta), by which time the CLI is waiting for the next body byte again. The turn ends at the CLI's agent_settled event. No timer decides a point or a result, so a loaded machine only makes the probe slower; a CLI that never reaches a point is stopped by the caller's deadline.
// Usage: node abort-probe.mjs <api|all> <node> <cli.js|binary> [out.json]
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const [apiArg, nodeBin, bin, outPath] = process.argv.slice(2);
let api;
const sse = (events) => events.map((e) => (e.event ? `event: ${e.event}\n` : '') + `data: ${JSON.stringify(e.data)}\n\n`).join('');
const firstRecords = {
	'openai-completions': () => sse([{ data: { id: 'c', model: 'strict', choices: [{ index: 0, delta: { content: 'a' }, finish_reason: null }] } }]),
	'mistral-conversations': () => sse([{ data: { id: 'c', model: 'strict', choices: [{ index: 0, delta: { content: 'a' }, finish_reason: null }] } }]),
	'openai-responses': () => sse([
		{ event: 'response.created', data: { type: 'response.created', response: { id: 'r1' } } },
		{ event: 'response.output_item.added', data: { type: 'response.output_item.added', output_index: 0, item: { type: 'message', id: 'm1', role: 'assistant', status: 'in_progress', content: [] } } },
		{ event: 'response.content_part.added', data: { type: 'response.content_part.added', output_index: 0, content_index: 0, part: { type: 'output_text', text: '', annotations: [] } } },
		{ event: 'response.output_text.delta', data: { type: 'response.output_text.delta', output_index: 0, content_index: 0, delta: 'a' } },
	]),
	'anthropic-messages': () => sse([
		{ event: 'message_start', data: { type: 'message_start', message: { id: 'msg_1', type: 'message', role: 'assistant', model: 'strict', content: [], stop_reason: null, usage: { input_tokens: 10, output_tokens: 1 } } } },
		{ event: 'content_block_start', data: { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } } },
		{ event: 'content_block_delta', data: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'a' } } },
	]),
	'google-generative-ai': () => sse([{ data: { candidates: [{ content: { role: 'model', parts: [{ text: 'a' }] } }], modelVersion: 'strict', responseId: 'g1' } }]),
};
const baseUrlFor = (port) => api === 'google-generative-ai' ? `http://127.0.0.1:${port}/v1beta` : api === 'anthropic-messages' ? `http://127.0.0.1:${port}` : `http://127.0.0.1:${port}/v1`;

async function run(phase) {
	let requested;
	const requestedPromise = new Promise((resolve) => { requested = resolve; });
	const sockets = new Set();
	const server = createServer(async (req, res) => {
		for await (const _ of req) { /* drain the request */ }
		if (phase === 'pre-headers') { requested(); return; }
		res.writeHead(200, { 'content-type': 'text/event-stream' });
		if (phase === 'mid-body') res.write(firstRecords[api]());
		else res.flushHeaders();
	});
	server.on('connection', (socket) => sockets.add(socket));
	await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
	const dir = mkdtempSync(join(tmpdir(), 'd82w7-'));
	const agent = join(dir, 'agent');
	mkdirSync(agent);
	writeFileSync(join(agent, 'models.json'), JSON.stringify({ providers: { p: { api, baseUrl: baseUrlFor(server.address().port), apiKey: 'k', models: [{ id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }] } } }));
	const isScript = bin.endsWith('.js');
	const child = spawn(isScript ? nodeBin : bin, [...(isScript ? [bin] : []), '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], { cwd: dir, env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, PIG_CODING_AGENT_DIR: agent, PIG_HOME: join(dir, 'pighome') }, stdio: ['pipe', 'pipe', 'inherit'] });
	child.stdin.on('error', () => { /* a CLI that exited is reported by gone below */ });
	const result = { phase, types: [] };
	const observers = new Set();
	const observe = (matches) => new Promise((resolve) => { observers.add({ matches, resolve }); });
	// The CLI's events the abort point and the end of the turn are read from. Both are registered before the prompt, so no event is missed.
	const reached = {
		'post-headers': (event) => event.type === 'message_start' && event.message.role === 'assistant',
		'mid-body': (event) => event.type === 'message_update' && event.assistantMessageEvent.type === 'text_delta',
	}[phase];
	const atPoint = phase === 'pre-headers' ? requestedPromise : observe(reached);
	const settled = observe((event) => event.type === 'agent_settled');
	createInterface({ input: child.stdout }).on('line', (line) => {
		let event;
		try { event = JSON.parse(line); } catch { return; }
		if (event.type === 'message_update') result.types.push(event.assistantMessageEvent.type);
		if (event.type === 'message_end' && event.message.role === 'assistant') {
			result.stopReason = event.message.stopReason;
			result.errorMessage = event.message.errorMessage;
			result.content = event.message.content.map((block) => block.text ?? block.type);
		}
		for (const observer of observers) if (observer.matches(event)) { observers.delete(observer); observer.resolve(); }
	});
	const gone = new Promise((_, reject) => child.once('close', (code, signal) => reject(new Error(`${bin} exited (${code ?? signal}) before ${phase} finished`))));
	gone.catch(() => { /* raised only where it is awaited */ });
	try {
		child.stdin.write(JSON.stringify({ id: 'p', type: 'prompt', message: 'hi' }) + '\n');
		await Promise.race([atPoint, gone]);
		child.stdin.write(JSON.stringify({ id: 'a', type: 'abort' }) + '\n');
		await Promise.race([settled, gone]);
	} finally {
		// Windows holds the child's cwd open until the process exits, so wait for the exit before removing the directory.
		const exited = new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) resolve(); else child.once('exit', resolve); });
		child.kill();
		for (const socket of sockets) socket.destroy();
		server.close();
		const escalate = setTimeout(() => child.kill('SIGKILL'), 5000);
		await exited;
		clearTimeout(escalate);
		rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
	}
	return result;
}

const apis = apiArg === 'all' ? Object.keys(firstRecords) : [apiArg];
const out = {};
for (api of apis) {
	out[api] = {};
	for (const phase of ['pre-headers', 'post-headers', 'mid-body']) out[api][phase] = await run(phase);
}
const text = JSON.stringify(out, null, 2) + '\n';
if (outPath) (await import('node:fs')).writeFileSync(outPath, text); else process.stdout.write(text);
