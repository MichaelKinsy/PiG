import { complete } from '@earendil-works/pi-ai/compat';
export default function (pi) {
 pi.registerCommand('native-options', {handler: async () => {
  const model={id:'gemini-2.5-pro',provider:'google',api:'google-generative-ai',baseUrl:'https://example.invalid',reasoning:true,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:1000,maxTokens:100};
  const result=await complete(model,{messages:[]},{apiKey:'request-key',thinking:{enabled:true,budgetTokens:0},reasoningEffort:'xhigh'});
  if(result.stopReason!=='stop') throw new Error(result.errorMessage);
 }});
}
