// Pi 1.0.1 / OpenAI 7.19.0. Kept structurally identical to the retained RPC33 probe.
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { writeFile, readFile, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const base = process.env.PI_PACKAGE_ROOT + '/node_modules/@earendil-works/';
for (const [path, version] of [[process.env.PI_PACKAGE_ROOT, '1.0.1'], [base + 'pi-ai', '1.0.1'], [base + 'pi-agent-core', '1.0.1'], [base + '../openai', '7.19.0']]) {
  assert.equal(JSON.parse(await readFile(path + '/package.json', 'utf8')).version, version);
}
const axes = {
  apis: ['openai-completions', 'openai-responses'],
  shapes: ['text', 'thinking', 'tool'],
  layers: [0, 1, 2, 'runtime'],
  modes: ['immediate-direct','delayed-direct','held-direct','result-only','cancel','immediate-agent','delayed-agent','held-agent','delayed-held-agent'],
};
const { runAgentLoop } = await import(base + 'pi-agent-core/dist/index.js');
const { lazyStream } = await import(base + 'pi-ai/dist/api/lazy.js');
const { ModelRuntime } = await import(process.env.PI_PACKAGE_ROOT + '/dist/core/model-runtime.js');
const providers = Object.fromEntries(await Promise.all(['openai-completions', 'openai-responses'].map(async api => [api, (await import(base + 'pi-ai/dist/api/' + api + '.js')).stream])));
const deferred = () => Promise.withResolvers();
const copy = value => JSON.parse(JSON.stringify(value));
const outputs = [];

function frames(api, shape) {
  const args = '{"path":"target.txt"}';
  if (api === 'openai-completions') {
    const chunks = shape === 'tool' ? [
      {choices:[{index:0,delta:{tool_calls:[{index:0,id:'call-r2',type:'function',function:{name:'read',arguments:args}}]},finish_reason:'tool_calls'}]},
    ] : shape === 'thinking' ? [
      {choices:[{index:0,delta:{reasoning_content:'one'},finish_reason:null}]},
      {choices:[{index:0,delta:{reasoning_content:' two'},finish_reason:null}]},
      {choices:[{index:0,delta:{content:'answer'},finish_reason:'stop'}]},
    ] : [
      {choices:[{index:0,delta:{content:'one'},finish_reason:null}]},
      {choices:[{index:0,delta:{content:' two'},finish_reason:'stop'}]},
    ];
    return [...chunks.map(c => 'data: ' + JSON.stringify({id:'response-r2',model:'probe',...c}) + '\n\n'), 'data: [DONE]\n\n'];
  }
  const item = shape === 'tool' ? {type:'function_call',id:'fc_r2',call_id:'call-r2',name:'read',arguments:''} : shape === 'thinking' ? {type:'reasoning',id:'rs_r2',summary:[]} : {type:'message',id:'msg_r2',role:'assistant',content:[]};
  const final = shape === 'tool' ? {...item,arguments:args} : shape === 'thinking' ? {...item,summary:[{type:'summary_text',text:'one two'}]} : {...item,content:[{type:'output_text',text:'one two',annotations:[]}]};
  const events = [
    {type:'response.created',response:{id:'response-r2'}},
    {type:'response.output_item.added',output_index:0,item},
    ...(shape === 'tool' ? [{type:'response.function_call_arguments.delta',output_index:0,delta:args}] : [{type:shape === 'thinking' ? 'response.reasoning_summary_text.delta' : 'response.output_text.delta',output_index:0,delta:'one'},{type:shape === 'thinking' ? 'response.reasoning_summary_text.delta' : 'response.output_text.delta',output_index:0,delta:' two'}]),
    {type:'response.output_item.done',output_index:0,item:final},
    {type:'response.completed',response:{id:'response-r2',status:'completed',output:[final]}},
  ];
  return events.map(e => 'event: ' + e.type + '\ndata: ' + JSON.stringify(e) + '\n\n');
}

async function probe(api, shape, mode, layers) {
  const first = deferred();
  const headers = deferred();
  const controller = new AbortController();
  const server = createServer(async (req, res) => {
    for await (const _ of req) {} // Finish request ownership before returning the response.
    res.writeHead(200, {'content-type':'text/event-stream'});
    if (mode.startsWith('delayed') || mode === 'cancel') {
      res.flushHeaders();
      headers.resolve();
      await first.promise;
    }
    if (res.destroyed) return;
    res.end(frames(api, shape).join(''));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const model = {id:'probe',name:'probe',api,provider:'probe-provider',baseUrl:`http://127.0.0.1:${server.address().port}/v1`,reasoning:false,input:['text'],contextWindow:4096,maxTokens:256,cost:{input:0,output:0,cacheRead:0,cacheWrite:0}};
  const message = {role:'user',content:[{type:'text',text:'probe'}],timestamp:1};
  const records = [];
  let runtime, runtimeDir;
  if (layers === 'runtime') {
    runtimeDir = await mkdtemp(join(tmpdir(), 'rpc33-runtime-'));
    const modelsPath = join(runtimeDir, 'models.json');
    await writeFile(modelsPath, JSON.stringify({providers:{'probe-provider':{apiKey:'test',baseUrl:model.baseUrl,api,models:[model]}}}));
    runtime = await ModelRuntime.create({modelsPath,authPath:join(runtimeDir,'auth.json'),refreshOnCreate:false});
  }
  let response, terminal;
  const streamFn = (m,c,o) => {
    if (runtime) return response = runtime.streamSimple(m,c,{...o,maxRetries:0,signal:controller.signal});
    let makeStream = () => providers[api](m,c,{...o,apiKey:'test',maxRetries:0,signal:controller.signal});
    for (let i=0; i<layers; i++) {
      const inner = makeStream;
      makeStream = () => lazyStream(m, async () => inner());
    }
    return response = makeStream();
  };
  const isAgent = mode.endsWith('agent');
  try {
    if (isAgent) {
      await runAgentLoop([message],{messages:[],tools:[]},{model,convertToLlm:m=>m,finishTurn:()=>({action:'end'})},async event => {
        records.push({at:'entry',event:copy(event)});
        if (event.type === 'message_start' && event.message.role === 'assistant') {
          first.resolve();
          if (mode.startsWith('held') || mode.startsWith('delayed-held')) {
            await response.result();
            records.push({at:'after-result',event:copy(event)});
          }
        }
      },controller.signal,streamFn);
    } else {
      response = streamFn(model,{messages:[message]},{});
      if (mode === 'result-only') {
        const result = await response.result();
        records.push({at:'result-before-iteration',result:copy(result)});
      }
      for await (const event of response) {
        if (event.type === 'done') terminal = event.message;
        if (event.type === 'error') terminal = event.error;
        records.push({at:'entry',event:copy(event)});
        if (event.type === 'start') {
          if (mode === 'cancel') controller.abort();
          first.resolve();
          if (mode === 'held-direct') {
            await response.result();
            records.push({at:'after-result',event:copy(event)});
          }
        }
      }
      const result = await response.result();
      assert.equal(result, terminal);
      assert.equal(await response.result(), result);
      records.push({at:'result',result:copy(result)});
      assert.equal(result.stopReason, mode === 'cancel' ? 'aborted' : shape === 'tool' ? 'toolUse' : 'stop');
      if (mode !== 'cancel') assert.ok(!JSON.stringify(result).match(/partialArgs|streamIndex|partialJson/));
      if (mode === 'delayed-direct') {
        assert.deepEqual(records[0].event.partial.content, []);
        assert.equal(records[0].event.partial.stopReason, 'pending');
      }
    }
  } finally {
    first.resolve();
    controller.abort();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    if (runtimeDir) await rm(runtimeDir,{recursive:true,force:true});
  }
  return {api,shape,mode,layers,records};
}
if (process.argv[3]) {
  await writeFile(process.argv[3], JSON.stringify({piVersion: '1.0.1', openaiVersion: '7.19.0', axes, bodies: Object.fromEntries(axes.apis.map(api => [api, Object.fromEntries(axes.shapes.map(shape => [shape, frames(api, shape).join('')]))]))}, null, 2) + '\n');
}
for (const layers of axes.layers) {
  for (const api of axes.apis) {
    for (const shape of axes.shapes) {
      for (const mode of axes.modes) {
        const output = await probe(api, shape, mode, layers);
        outputs.push(output);
        console.log(JSON.stringify(output));
      }
    }
  }
}
await writeFile(process.argv[2], JSON.stringify(outputs,null,2)+'\n');
