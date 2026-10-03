// Runs a Pi-compatible CLI in --mode rpc against a loopback OpenAI-completions server whose whole body arrives in one write, and prints the assistant message frames.
// Each SSE chunk carries a growing usage. The `usage` a message_update frame shows (rpc-mode.ts:354-363 serializes event.message.usage, a reference the provider keeps mutating)
// depends on how far the provider ran before that frame was written, so it exposes the stdout-backpressure listener at rpc-mode.ts:361-363.
// Usage: node rpc-usage-probe.mjs <node> <cli-entry-or-binary> [out.json] [rpc|json]   (json runs `--mode json -p`, print-mode.ts:108-118)
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const [nodeBin, cli, outPath, mode = 'rpc'] = process.argv.slice(2);
const chunk = (content, output, finish) => 'data: ' + JSON.stringify({ id: 'chatcmpl-w7', model: 'strict', choices: [{ index: 0, delta: content === undefined ? {} : { content }, finish_reason: finish ?? null }], usage: { prompt_tokens: 10, completion_tokens: output, total_tokens: 10 + output } }) + '\n\n';
const body = Buffer.from([chunk('a', 1), chunk('b', 2), chunk('c', 3), chunk('d', 4), chunk('e', 5, 'stop'), 'data: [DONE]\n\n'].join(''));
const server = createServer(async (req, res) => {
	for await (const _ of req) { /* drain */ }
	res.writeHead(200, { 'content-type': 'text/event-stream', 'content-length': body.length });
	res.end(body);
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const dir = mkdtempSync(join(tmpdir(), 'd82w7-'));
const agent = join(dir, 'agent');
mkdirSync(agent);
writeFileSync(join(agent, 'models.json'), JSON.stringify({ providers: { p: { api: 'openai-completions', baseUrl: `http://127.0.0.1:${server.address().port}/v1`, apiKey: 'k', models: [{ id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }] } } }));
const isScript = cli.endsWith('.js');
const child = spawn(isScript ? nodeBin : cli, [...(isScript ? [cli] : []), '--mode', mode, ...(mode === 'json' ? ['-p', 'hi'] : []), '--offline', '--no-extensions', ...(process.env.PROBE_EXTENSION ? ['--extension', process.env.PROBE_EXTENSION] : []), '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], { cwd: dir, env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, PIG_CODING_AGENT_DIR: agent, PIG_HOME: join(dir, 'pighome') }, stdio: ['pipe', 'pipe', 'inherit'] });
const frames = [];
await new Promise((resolve) => {
	const lines = createInterface({ input: child.stdout });
	lines.on('close', resolve);
	lines.on('line', (line) => {
		if (process.env.PROBE_DEBUG) console.error(line.slice(0, 200));
		let event;
		try { event = JSON.parse(line); } catch { return; }
		const message = event.message;
		if (message?.role === 'assistant' && (event.type === 'message_start' || event.type === 'message_update' || event.type === 'message_end')) {
			frames.push({ type: event.type, delta: event.assistantMessageEvent?.type === 'text_delta' ? event.assistantMessageEvent.delta : undefined, outputTokens: (event.usage ?? message.usage)?.output, stopReason: message.stopReason, text: message.content?.map((block) => block.text).join('') });
		} else if (event.type === 'message_update' && event.usage) {
			frames.push({ type: event.type, delta: event.assistantMessageEvent?.delta, outputTokens: event.usage.output });
		}
		if (event.type === 'agent_settled') resolve();
	});
	if (mode === 'rpc') child.stdin.write(JSON.stringify({ id: 'p', type: 'prompt', message: 'hi' }) + '\n');
	else child.stdin.end();
	setTimeout(resolve, 20000).unref();
});
// The child runs with dir as its cwd; Windows refuses to remove a directory a live process is in, so wait for it to exit.
const exited = child.exitCode !== null || child.signalCode !== null ? Promise.resolve() : new Promise((resolve) => child.once('exit', resolve));
child.kill();
await exited;
server.close();
rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
const text = JSON.stringify(frames, null, 2) + '\n';
if (outPath) writeFileSync(outPath, text); else process.stdout.write(text);
