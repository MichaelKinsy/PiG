import {getModel,stream} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
const rows=[];
for(const [id,options] of [['claude-haiku-4-5',{thinkingEnabled:true}],['claude-haiku-4-5',{thinkingEnabled:true,thinkingBudgetTokens:2048}],['claude-opus-4-6',{thinkingEnabled:true,effort:'medium'}]]){
 await stream(getModel('anthropic',id),{messages:[{role:'user',content:'hi',timestamp:1}]},{...options,apiKey:'test',onPayload:body=>{rows.push([id,body.thinking?.type??null,body.thinking?.budget_tokens??null,body.thinking?.display??null,body.output_config?.effort??null]);throw new Error('captured before network');}}).result();
}
for(const metadata of [{app:'pi-test',env:'ci'},undefined]){
 await stream(getModel('amazon-bedrock','global.anthropic.claude-sonnet-4-5-20250929-v1:0'),{messages:[{role:'user',content:'Say hi.',timestamp:1}]},{env:{AWS_BEDROCK_SKIP_AUTH:'1',AWS_REGION:'us-east-1'},requestMetadata:metadata,onPayload:body=>{rows.push(['bedrock',body.requestMetadata??null]);throw new Error('captured before network');}}).result();
}
console.log(JSON.stringify(rows));
