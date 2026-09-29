// Azure OpenAI Responses microtask-tick probe. Pi 0.87.1 / OpenAI 6.40.0.
// Usage: node ticks.mjs <ticks.json>
//
// Drives the real Pi provider (packages/ai/src/api/azure-openai-responses.ts) and, for the runtime path, the real
// ModelRuntime.streamSimple (coding-agent/src/core/model-runtime.ts:638-643, two lazyStream layers on top of the provider).
// A consumer iterates the EventStream with `for await` (ai/src/utils/event-stream.ts:44-91).
//
// Tick measurement: a self-rescheduling queueMicrotask chain adds exactly one microtask per FIFO generation and adds no
// await or reaction to Pi's code, so it never reorders Pi's reactions. The chain restarts at every delivery. `gap` is the
// number of chain steps that ran between the previous delivery's synchronous body and this delivery, i.e. the microtask
// generations Pi needed to reach the next event. The first gap is measured from the synchronous `onResponse` hook
// (azure-openai-responses.ts:127-128, `await options?.onResponse?.(...)` then `stream.push(start)`).
// A gap that reaches GAP_CAP means a macrotask (socket read) intervened; it is recorded as `io: true`.
//
// Fixtures (server writes are real sockets):
//   buffered  headers and the complete body in one write; the body is already in the client's socket buffer at `start`.
//   pending   headers flushed alone; the whole body is written in a later macrotask after the consumer sees `start`.
//   chunked   headers flushed; each SSE record is its own write, one macrotask apart, released after `start`.
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { writeFile, readFile, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const root = process.env.PI_PACKAGE_ROOT;
const base = root + '/node_modules/@earendil-works/';
for (const [path, version] of [[root, '0.87.1'], [base + 'pi-ai', '0.87.1'], [base + 'pi-agent-core', '0.87.1'], [root + '/node_modules/openai', '6.40.0']]) {
  assert.equal(JSON.parse(await readFile(path + '/package.json', 'utf8')).version, version);
}
const GAP_CAP = 64;
const axes = { layers: [0, 'runtime'], shapes: ['text', 'thinking', 'tool'], fixtures: ['buffered', 'pending', 'chunked'] };
const { ModelRuntime } = await import(root + '/dist/core/model-runtime.js');
const provider = (await import(base + 'pi-ai/dist/api/azure-openai-responses.js')).stream;
const copy = value => JSON.parse(JSON.stringify(value));

function records(shape) {
  const args = '{"path":"target.txt"}';
  const item = shape === 'tool' ? {type:'function_call',id:'fc_r2',call_id:'call-r2',name:'read',arguments:''} : shape === 'thinking' ? {type:'reasoning',id:'rs_r2',summary:[]} : {type:'message',id:'msg_r2',role:'assistant',content:[]};
  const final = shape === 'tool' ? {...item,arguments:args} : shape === 'thinking' ? {...item,summary:[{type:'summary_text',text:'one two'}]} : {...item,content:[{type:'output_text',text:'one two',annotations:[]}]};
  const delta = shape === 'thinking' ? 'response.reasoning_summary_text.delta' : 'response.output_text.delta';
  const events = [
    {type:'response.created',response:{id:'response-r2'}},
    {type:'response.output_item.added',output_index:0,item},
    ...(shape === 'tool' ? [{type:'response.function_call_arguments.delta',output_index:0,delta:args}] : [{type:delta,output_index:0,delta:'one'},{type:delta,output_index:0,delta:' two'}]),
    {type:'response.output_item.done',output_index:0,item:final},
    {type:'response.completed',response:{id:'response-r2',status:'completed',output:[final]}},
  ];
  return events.map(e => 'event: ' + e.type + '\ndata: ' + JSON.stringify(e) + '\n\n');
}

async function probe(layers, shape, fixture) {
  const start = Promise.withResolvers();
  const server = createServer(async (req, res) => {
    for await (const _ of req) {}
    res.writeHead(200, {'content-type':'text/event-stream'});
    const recs = records(shape);
    if (fixture === 'buffered') { res.end(recs.join('')); return; }
    res.flushHeaders();
    await start.promise;
    if (res.destroyed) return;
    if (fixture === 'pending') { res.end(recs.join('')); return; }
    for (const r of recs) { await new Promise(resolve => setTimeout(resolve, 5)); res.write(r); }
    res.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const model = {id:'probe',name:'probe',api:'azure-openai-responses',provider:'probe-provider',baseUrl:`http://127.0.0.1:${server.address().port}/v1`,reasoning:false,input:['text'],contextWindow:4096,maxTokens:256,cost:{input:0,output:0,cacheRead:0,cacheWrite:0}};
  let runtime, runtimeDir;
  if (layers === 'runtime') {
    runtimeDir = await mkdtemp(join(tmpdir(), 'azure-ticks-'));
    const modelsPath = join(runtimeDir, 'models.json');
    await writeFile(modelsPath, JSON.stringify({providers:{'probe-provider':{apiKey:'test',baseUrl:model.baseUrl,api:'azure-openai-responses',models:[model]}}}));
    runtime = await ModelRuntime.create({modelsPath,authPath:join(runtimeDir,'auth.json'),refreshOnCreate:false});
  }
  let gen = 0, n = 0;
  const mark = () => {
    const previous = n, mine = ++gen;
    n = 0;
    const spin = () => { if (mine !== gen) return; n++; if (n < GAP_CAP) queueMicrotask(spin); };
    queueMicrotask(spin);
    return previous;
  };
  let firstGap;
  const opts = { maxRetries: 0, onResponse: () => { mark(); } };
  const context = {messages:[{role:'user',content:[{type:'text',text:'probe'}],timestamp:1}]};
  const stream = runtime ? runtime.streamSimple(runtime.getModel ? runtime.getModel('probe-provider','probe') : model, context, opts) : provider(model, context, {...opts, apiKey:'test'});
  const out = [];
  let first = true;
  for await (const event of stream) {
    const gap = mark();
    const partial = event.partial ?? event.message ?? event.error;
    out.push({type:event.type, ...(event.contentIndex !== undefined ? {contentIndex:event.contentIndex} : {}), ...(event.delta !== undefined ? {delta:event.delta} : {}), gap: Math.min(gap, GAP_CAP), io: gap >= GAP_CAP - 1, partial: copy(partial)});
    if (event.type === 'start') start.resolve();
  }
  gen++;
  start.resolve();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  if (runtimeDir) await rm(runtimeDir, {recursive:true,force:true});
  return {api:'azure-openai-responses', layers, shape, fixture, events: out};
}

const results = [];
for (const layers of axes.layers) for (const shape of axes.shapes) for (const fixture of axes.fixtures) {
  const r = await probe(layers, shape, fixture);
  results.push(r);
  console.log(JSON.stringify({layers, shape, fixture, gaps: r.events.map(e => e.type + ':' + e.gap + (e.io ? '*' : ''))}));
}
await writeFile(process.argv[2], JSON.stringify({piVersion:'0.87.1', openaiVersion:'6.40.0', gapCap:GAP_CAP, axes, bodies:Object.fromEntries(axes.shapes.map(shape => [shape, records(shape)])), results}, null, 2) + '\n');
