import { getModel, stream } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
const rows=[];
for(const api of ['openai-responses','openai-completions']){
 let captured;
 const base=getModel('openai','gpt-5-mini');
 const model={...base,api,compat:api==='openai-completions'?undefined:base.compat};
 const fetch=async(_url,init)=>{
  const body=JSON.parse(init.body);
  captured=body.reasoning?.effort ?? body.reasoning_effort;
  if(captured==='xhigh') return new Response('{"error":{"message":"Unsupported value: xhigh","type":"invalid_request_error"}}',{status:400,headers:{'content-type':'application/json'}});
  return new Response(api==='openai-responses'?'data: {"type":"response.completed","response":{"status":"completed"}}\n\n':'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',{headers:{'content-type':'text/event-stream'}});
 };
 const result=await stream(model,{messages:[{role:'user',content:'What is 17 + 23? Think step by step.',timestamp:1}]},{apiKey:'test',reasoningEffort:'xhigh',fetch}).result();
 rows.push([api,captured,result.stopReason,result.errorMessage?.includes('xhigh')??false]);
}
for(const effort of ['xhigh','high','max']){
 let captured;
 const fetch=async(_url,init)=>{
  captured=JSON.parse(init.body);
  return new Response('data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',{headers:{'content-type':'text/event-stream'}});
 };
 const result=await stream(getModel('zai','glm-5.2'),{messages:[{role:'user',content:'hi',timestamp:1}]},{apiKey:'test',reasoningEffort:effort,fetch}).result();
 rows.push(['zai',effort,captured.thinking?.type??null,captured.thinking?.clear_thinking??null,captured.reasoning_effort??null,result.stopReason]);
}
console.log(JSON.stringify(rows));
