// Compare the request fields guarded by packages/ai/test/fireworks-models.test.ts.
import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const aiRoot = join(root, 'node_modules/@earendil-works/pi-ai');
if (JSON.parse(readFileSync(join(aiRoot, 'package.json'), 'utf8')).version !== '1.0.3') throw new Error('expected Pi AI 1.0.3');
const mod = (path) => import(pathToFileURL(join(aiRoot, 'dist', path)).href);
const { getModel, normalizeContext } = await mod('compat.js');
const anthropic = await mod('api/anthropic-messages.js');
const completions = await mod('api/openai-completions.js');
const context = normalizeContext({messages:[{role:'user',content:'Use the tool',timestamp:0}],tools:[{name:'lookup',description:'Look up a value',parameters:{type:'object',properties:{value:{type:'string'}},required:['value']}}]});
const records = [];
for (const [provider, cacheRetention, sessionId, compat, envRetention = ''] of [
  ['fireworks','short','fireworks-session-1',{sendSessionAffinityHeaders:true,supportsCacheControlOnTools:false,supportsEagerToolInputStreaming:false}],
  ['fireworks','none','fireworks-session-2',{sendSessionAffinityHeaders:true,supportsCacheControlOnTools:false,supportsEagerToolInputStreaming:false}],
  ['openrouter','short','openrouter-session-1',{}],
  ['openrouter','none','openrouter-session-2',{}],
  ['openrouter','short','openrouter-session-3',{sendSessionAffinityHeaders:false}],
  ['anthropic','short','anthropic-session-1',{}],
  ['openrouter','','env-none',{},'none'],
  ['openrouter','','env-long',{},'long'],
  ['openrouter','none','explicit-none',{},'long'],
]) {
  let record;
  const server = createServer(async (req,res) => {
    const chunks=[]; for await (const chunk of req) chunks.push(chunk);
    const body=JSON.parse(Buffer.concat(chunks));
    record={provider,retention:cacheRetention,session:sessionId,affinity:req.headers['x-session-affinity']??'',sessionId:req.headers['x-session-id']??'',tools:body.tools,messages:body.messages};
    res.writeHead(200,{'Content-Type':'text/event-stream'}); res.end();
  });
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  try {
    const model={id:provider==='fireworks'?'accounts/fireworks/models/kimi-k2p6':provider==='openrouter'?'anthropic/claude-opus-4.8':'claude-opus-4-8',name:'test',api:'anthropic-messages',provider,baseUrl:`http://127.0.0.1:${server.address().port}`,reasoning:true,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:200000,maxTokens:32000,compat};
    await anthropic.stream(model,context,{apiKey:'test-key',cacheRetention:cacheRetention||undefined,sessionId,env:{PI_CACHE_RETENTION:envRetention}}).result();
    if (!record) throw new Error('request not captured');
    records.push(record);
  } finally { await new Promise(resolve => server.close(resolve)); }
}
let effort;
await completions.streamSimple(getModel('fireworks','accounts/fireworks/models/kimi-k3'),context,{apiKey:'test-key',reasoning:'max',onPayload:p=>{effort=p.reasoning_effort;throw new Error('captured');}}).result();
records.push({provider:'fireworks-kimi-k3',effort:effort??null});
function sorted(value) { if(Array.isArray(value)) return value.map(sorted); if(value&&typeof value==='object') return Object.fromEntries(Object.keys(value).sort().map(k=>[k,sorted(value[k])])); return value; }
console.log(JSON.stringify(sorted(records)));
