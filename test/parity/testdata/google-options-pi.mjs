import {getModel,stream} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
const model=getModel('google','gemini-2.5-pro');
const rows=[];
for(const thinking of [{enabled:true},{enabled:true,budgetTokens:1024},{enabled:true,budgetTokens:0},{enabled:true,budgetTokens:1024,level:'LOW'},{enabled:false}]){
 let captured;
 const original=globalThis.fetch;
 globalThis.fetch=async(input,init)=>{captured=JSON.parse(await new Request(input,init).text());return new Response('data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}\n\n',{headers:{'content-type':'text/event-stream'}})};
 try {
  const result=await stream(model,{messages:[{role:'user',content:'hi',timestamp:1}]},{apiKey:'test',thinking}).result();
  if(result.stopReason!=='stop')throw new Error(result.errorMessage);
  const cfg=captured.generationConfig.thinkingConfig;
  rows.push([cfg.includeThoughts??null,cfg.thinkingBudget??null,cfg.thinkingLevel??null]);
 } finally {globalThis.fetch=original;}
}
console.log(JSON.stringify(rows));
