import { getModel, stream } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
function findText(value){
 if(typeof value==='string'&&value.includes('No result provided')) return value;
 if(value&&typeof value==='object') for(const child of Object.values(value)){const text=findText(child);if(text) return text;}
 return '';
}
const rows=[];
for(const [api,provider,id] of [['openai-completions','openai','gpt-4o-mini'],['openai-responses','openai','gpt-5-mini'],['anthropic-messages','anthropic','claude-haiku-4-5'],['google-generative-ai','google','gemini-2.5-flash'],['mistral-conversations','mistral','devstral-medium-latest']]){
 let captured, found=false, content=null;
 const model={...getModel(provider,id),api};
 if(api==='openai-completions')delete model.compat;
 const fetch=async(input,init)=>{
  const body=JSON.parse(await new Request(input,init).text());
  captured=findText(body);
  if(api==='openai-completions') for(const message of body.messages){
   if(message.role==='assistant'&&message.tool_calls){found=true;content=message.content;}
  }
  let reply='data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n';
  if(api==='openai-responses')reply='data: {"type":"response.completed","response":{"status":"completed"}}\n\n';
  if(api==='anthropic-messages')reply='event: message_start\ndata: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":1}}}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n';
  if(api==='google-generative-ai')reply='data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}\n\n';
  return new Response(reply,{headers:{'content-type':'text/event-stream'}});
 };
 const request={systemPrompt:'You are a helpful assistant. Use the calculate tool when asked to perform calculations.',tools:[{name:'calculate',description:'Evaluate mathematical expressions',parameters:{type:'object',properties:{expression:{type:'string',description:'The mathematical expression to evaluate'}},required:['expression']}}],messages:[
  {role:'user',content:'Please calculate 25 * 18 using the calculate tool.',timestamp:1},
  {role:'assistant',content:[{type:'toolCall',id:'calc00001',name:'calculate',arguments:{expression:'25 * 18'}}],api,provider,model:id,stopReason:'toolUse',timestamp:2,usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}}},
  {role:'user',content:'Never mind, just tell me what is 2+2?',timestamp:4}
 ]};
 const originalFetch=globalThis.fetch;
 globalThis.fetch=fetch;
 let result;
 try { result=await stream(model,request,{apiKey:'test',fetch}).result(); }
 finally { globalThis.fetch=originalFetch; }
 if(result.stopReason==='error') throw new Error(result.errorMessage);
 if(request.messages.length!==3)throw new Error('provider mutated history');
 rows.push([api,captured,result.stopReason,found,content]);
}
console.log(JSON.stringify(rows));
