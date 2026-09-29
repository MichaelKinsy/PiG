import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai=join(root,'node_modules/@earendil-works/pi-ai/dist');
const {normalizeContext}=await import(pathToFileURL(join(ai,'compat.js')));
const output={};
for(const api of ['anthropic-messages','azure-openai-responses','google-generative-ai','mistral-conversations','openai-codex-responses','openai-completions','openai-responses']){
 const {streamSimple}=await import(pathToFileURL(join(ai,`api/${api}.js`)));
 const model={id:'test-model',name:'Test',api,provider:'test-provider',baseUrl:'https://example.invalid',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:1000,maxTokens:100};
 try {streamSimple(model,normalizeContext({messages:[]}),{});}catch(error){output[api]=error.message;continue;}
 throw new Error(`${api} did not reject synchronously`);
}
console.log(JSON.stringify(Object.fromEntries(Object.entries(output).sort(([a],[b])=>a.localeCompare(b)))));
