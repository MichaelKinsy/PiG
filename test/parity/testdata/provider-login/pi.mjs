import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai=join(root,'node_modules/@earendil-works/pi-ai/dist');
const {envApiKeyAuth}=await import(pathToFileURL(join(ai,'auth/helpers.js')));
const {amazonBedrockProvider}=await import(pathToFileURL(join(ai,'providers/amazon-bedrock.js')));
const {googleVertexProvider}=await import(pathToFileURL(join(ai,'providers/google-vertex.js')));
const output={};
for(const [name,auth,answers] of [['env',envApiKeyAuth('Test key',['TEST_KEY']),['entered-key']],['bedrock-bearer',amazonBedrockProvider().auth.apiKey,['bearer-token','bedrock-token']],['bedrock-profile',amazonBedrockProvider().auth.apiKey,['aws-profile','work']],['vertex-key',googleVertexProvider().auth.apiKey,['api-key','vertex-key']],['vertex-adc',googleVertexProvider().auth.apiKey,['adc','project-id','us-central1']]]){
 const prompts=[],events=[];
 const credential=await auth.login({signal:new AbortController().signal,prompt:async prompt=>{prompts.push(prompt);if(!answers.length)throw new Error('unexpected prompt');return answers.shift();},notify:event=>events.push(event)});
 output[name]={credential,prompts,events};
}
const sorted=value=>Array.isArray(value)?value.map(sorted):value&&typeof value==='object'?Object.fromEntries(Object.entries(value).sort(([a],[b])=>a.localeCompare(b)).map(([key,value])=>[key,sorted(value)])):value;
console.log(JSON.stringify(sorted(output)));
