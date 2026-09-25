#!/usr/bin/env node
// orphaned-tool-result-pi.mjs drives the pinned upstream openai-completions
// provider for the D48 parity scenario. It builds the same poisoned conversation
// as the pig harness (an aborted assistant turn holding the only tool_use for a
// persisted tool-result), streams it through the real provider against a
// hermetic endpoint, and prints the tool_call_ids the serialized request carries.
// Upstream transformMessages drops the aborted assistant but never strips the
// now-orphaned tool-result, so the request still carries call_x.
import { createServer } from 'node:http';
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
const aiRoot = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const openaiCompletions = await import(pathToFileURL(join(aiRoot, 'api/openai-completions.js')).href);

async function captureServer() {
  const cap = {};
  const server = createServer((req, res) => {
    let body = '';
    req.on('data', (c) => { body += c; });
    req.on('end', () => {
      cap.body = body;
      res.writeHead(418, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: { message: 'probe stop' } }));
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return { server, cap, url: `http://127.0.0.1:${server.address().port}` };
}

function poisonedContext() {
  return {
    systemPrompt: undefined,
    messages: [
      { role: 'user', content: [{ type: 'text', text: 'run it' }] },
      { role: 'assistant', stopReason: 'error', content: [{ type: 'toolCall', id: 'call_x', name: 'bash', arguments: { command: 'expr 20 + 22' } }] },
      { role: 'toolResult', toolCallId: 'call_x', toolName: 'bash', content: [{ type: 'text', text: '42\n' }], isError: false, timestamp: 0 },
    ],
    tools: [],
  };
}

function toolCallIds(body) {
  let req;
  try { req = JSON.parse(body); } catch { return '[unparseable]'; }
  const ids = (req.messages ?? [])
    .filter((m) => m.role === 'tool' && m.tool_call_id)
    .map((m) => m.tool_call_id)
    .sort();
  return `[${ids.join(' ')}]`;
}

const { server, cap, url } = await captureServer();
const model = {
  id: 'gpt-4o-mini', name: 'gpt-4o-mini', provider: 'openai', api: 'openai-completions', baseUrl: url,
  input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  contextWindow: 128000, maxTokens: 4096, headers: {}, reasoning: false,
};
const stream = openaiCompletions.stream(model, poisonedContext(), { apiKey: 'sk-test' });
for await (const _ of stream) { /* drain */ }
await new Promise((resolve) => server.close(resolve));

if (!cap.body) { console.error('no request body captured'); process.exit(1); }
console.log(`tool_call_ids_in_request: ${toolCallIds(cap.body)}`);
