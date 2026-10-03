import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { getEffortThinkingLevelMap } from '../../../../.upstream/current/packages/ai/scripts/models-dev-reasoning-options.ts';
import { getOpenRouterThinkingLevelMap } from '../../../../.upstream/current/packages/ai/scripts/openrouter-reasoning-options.ts';
import { pathToFileURL } from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai=join(root,'node_modules/@earendil-works/pi-ai/dist');
const {normalizeContext}=await import(pathToFileURL(join(ai,'compat.js')));
const mapping={off:null,minimal:null,low:'low',medium:null,high:'high',xhigh:null,max:'max'};
const output={};
for(const api of ['openai-completions','openai-responses']){
 const {streamSimple}=await import(pathToFileURL(join(ai,`api/${api}.js`)));
 for(const name of ['mandatory-unset','mandatory-low','optional-unset']){
  const model={id:api==='openai-completions'?'stealth/ox-alpha':'mapped',name:'Model',api,provider:api==='openai-completions'?'openrouter':'openai',baseUrl:'https://example.invalid/v1',reasoning:true,thinkingLevelMap:name==='optional-unset'?undefined:mapping,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096,compat:api==='openai-completions'?{thinkingFormat:'openrouter'}:undefined};
  let captured=false;
  await streamSimple(model,normalizeContext({messages:[{role:'user',content:'Hello',timestamp:0}]}),{apiKey:'test',reasoning:name==='mandatory-low'?'low':undefined,onPayload(payload){output[`${api}/${name}`]=payload.reasoning??null;captured=true;throw new Error('captured');}}).result();
  if(!captured)throw new Error('no captured payload');
 }
}
for (const test of JSON.parse(readFileSync('test/parity/testdata/reasoning-options/metadata.json', 'utf8'))) {
 output[`metadata/${test.source}/${test.name}`] = (test.source === 'models-dev' ? getEffortThinkingLevelMap(test.value) : getOpenRouterThinkingLevelMap(test.value)) ?? null;
}
const sorted=value=>value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(key=>[key,sorted(value[key])])):value;
console.log(JSON.stringify(sorted(output)));
