import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const {transformMessages}=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-ai/dist/api/transform-messages.js')));
const model={id:'test-model',name:'Test Model',api:'openai-completions',provider:'openai',baseUrl:'https://example.invalid/v1',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:16000};
const messages=[{role:'user',content:null,timestamp:0},{role:'assistant',content:null,api:model.api,provider:model.provider,model:model.id,usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:0},{role:'toolResult',toolCallId:'call_1',toolName:'web_search',isError:false,timestamp:0}];
console.log(JSON.stringify(transformMessages(messages,model).map(message=>message.content)));
