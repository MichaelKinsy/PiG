// Azure OpenAI Responses start state through the real Pi CLI RPC path. Pi 1.0.1 / OpenAI 7.19.0.
// Usage: node rpc.mjs <rpc.json> [runs]
//
// Runs `pi --mode rpc` (dist/bundle/cli.js) against one loopback server whose provider is api "azure-openai-responses".
// The server answers request 1 with the RPC33 tool call (or a text reply) and every later request with plain text, so the run
// ends. The observation is the first assistant message's ordered RPC records: message_start, every message_update, message_end.
//   buffered  headers and the complete body in a single write (the RPC33 fixture shape).
//   pending   headers flushed; the body is written when the driver reads the first assistant message_start.
// Every run must be identical after numeric message-timestamp canonicalization; the probe fails otherwise.
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, mkdirSync, rmSync, readFileSync } from 'node:fs';
import { writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const root = process.env.PI_PACKAGE_ROOT;
assert.equal(JSON.parse(readFileSync(root + '/package.json', 'utf8')).version, '1.0.1');
const runs = Number(process.argv[3] ?? 8);
const args = '{"path":"parity-read-target.txt"}';
const sse = evs => evs.map(e => 'event: ' + e.type + '\ndata: ' + JSON.stringify(e) + '\n\n').join('');
const bodies = {
  tool: sse([
    {type:'response.created',response:{id:'resp_1'}},
    {type:'response.output_item.added',output_index:0,item:{type:'function_call',id:'fc_1',call_id:'call_1',name:'read',arguments:''}},
    {type:'response.function_call_arguments.delta',output_index:0,delta:args},
    {type:'response.output_item.done',output_index:0,item:{type:'function_call',id:'fc_1',call_id:'call_1',name:'read',arguments:args,status:'completed'}},
    {type:'response.completed',response:{id:'resp_1',status:'completed',output:[{type:'function_call',id:'fc_1',call_id:'call_1',name:'read',arguments:args,status:'completed'}],usage:{input_tokens:10,output_tokens:3,total_tokens:13}}},
  ]),
  text: sse([
    {type:'response.created',response:{id:'resp_1'}},
    {type:'response.output_item.added',output_index:0,item:{type:'message',id:'msg_1',role:'assistant',status:'in_progress',content:[]}},
    {type:'response.output_text.delta',output_index:0,delta:'one'},
    {type:'response.output_text.delta',output_index:0,delta:' two'},
    {type:'response.output_item.done',output_index:0,item:{type:'message',id:'msg_1',role:'assistant',status:'completed',content:[{type:'output_text',text:'one two',annotations:[]}]}},
    {type:'response.completed',response:{id:'resp_1',status:'completed',output:[{type:'message',id:'msg_1',role:'assistant',status:'completed',content:[{type:'output_text',text:'one two',annotations:[]}]}],usage:{input_tokens:10,output_tokens:3,total_tokens:13}}},
  ]),
  followup: sse([
    {type:'response.created',response:{id:'resp_2'}},
    {type:'response.output_item.added',output_index:0,item:{type:'message',id:'msg_2',role:'assistant',status:'in_progress',content:[]}},
    {type:'response.output_text.delta',output_index:0,delta:'ok'},
    {type:'response.output_item.done',output_index:0,item:{type:'message',id:'msg_2',role:'assistant',status:'completed',content:[{type:'output_text',text:'ok',annotations:[]}]}},
    {type:'response.completed',response:{id:'resp_2',status:'completed',output:[{type:'message',id:'msg_2',role:'assistant',status:'completed',content:[{type:'output_text',text:'ok',annotations:[]}]}]}},
  ]),
};
const canon = value => JSON.parse(JSON.stringify(value, (k, v) => (k === 'timestamp' && typeof v === 'number' && v > 1e9 ? 0 : v)));

async function once1(shape, fixture) {
  const release = Promise.withResolvers();
  let requests = 0;
  const server = createServer(async (req, res) => {
    for await (const _ of req) {}
    const first = ++requests === 1;
    const buf = Buffer.from(first ? bodies[shape] : bodies.followup);
    if (fixture === 'buffered' || !first) {
      res.writeHead(200, {'content-type':'text/event-stream','content-length':buf.length});
      res.end(buf);
      return;
    }
    res.writeHead(200, {'content-type':'text/event-stream'});
    res.flushHeaders();
    await release.promise;
    if (!res.destroyed) res.end(buf);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const dir = mkdtempSync(join(tmpdir(), 'azure-rpc-'));
  const agent = join(dir, 'agent'); mkdirSync(agent);
  writeFileSync(join(agent, 'models.json'), JSON.stringify({providers:{p:{api:'azure-openai-responses', baseUrl:`http://127.0.0.1:${server.address().port}/openai/v1`, apiKey:'k', models:[{id:'strict',name:'strict',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:1000}]}}}));
  writeFileSync(join(dir, 'parity-read-target.txt'), 'x\n');
  const pi = spawn(process.execPath, [root + '/dist/bundle/cli.js', '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], {cwd: dir, env: {...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent}, stdio:['pipe','pipe','inherit']});
  const records = [];
  let assistantOpen = false, done = false;
  const finished = new Promise(resolve => {
    createInterface({input: pi.stdout}).on('line', line => {
      let e; try { e = JSON.parse(line); } catch { return; }
      if (done) return;
      const assistant = e.message?.role === 'assistant';
      if (e.type === 'message_start' && assistant && !assistantOpen) { assistantOpen = true; release.resolve(); }
      if (assistantOpen && (e.type === 'message_update' || (['message_start', 'message_end'].includes(e.type) && assistant))) records.push(e);
      if (e.type === 'message_end' && assistant && assistantOpen) { done = true; resolve(); }
    });
    setTimeout(resolve, 20000);
  });
  pi.stdin.write(JSON.stringify({id:'read',type:'prompt',message:'READ'}) + '\n');
  await finished;
  release.resolve();
  pi.kill(); server.closeAllConnections(); server.close(); rmSync(dir, {recursive:true, force:true});
  assert.ok(done, 'assistant message_end not observed');
  return canon(records);
}

const out = [];
for (const shape of ['tool', 'text']) for (const fixture of ['buffered', 'pending']) {
  let first;
  for (let i = 0; i < runs; i++) {
    const r = await once1(shape, fixture);
    if (i === 0) first = r; else assert.deepEqual(r, first, `${shape}/${fixture} run ${i} differs`);
  }
  const start = first[0].message;
  console.log(JSON.stringify({shape, fixture, runs, start: {content: start.content, stopReason: start.stopReason, responseId: start.responseId, total: start.usage.totalTokens}, updates: first.length - 2}));
  out.push({api:'azure-openai-responses', shape, fixture, runs, records: first});
}
await writeFile(process.argv[2], JSON.stringify({piVersion:'1.0.1', openaiVersion:'7.19.0', bodies, cases: out}, null, 2) + '\n');
