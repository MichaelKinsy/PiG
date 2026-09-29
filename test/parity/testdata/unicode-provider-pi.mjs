import { getModel, stream } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
function findText(value){
 if(typeof value==='string'&&value.includes('Text with unpaired surrogate:')) return value;
 if(value&&typeof value==='object') for(const child of Object.values(value)){const text=findText(child);if(text) return text;}
 return '';
}
const rows=[];
for(const [api,provider,id] of [['openai-completions','openai','gpt-4o-mini'],['openai-responses','openai','gpt-5-mini'],['anthropic-messages','anthropic','claude-haiku-4-5'],['google-generative-ai','google','gemini-2.5-flash'],['mistral-conversations','mistral','devstral-medium-latest']]){
 let captured;
 const model={...getModel(provider,id),api};
 if(api==='openai-completions')delete model.compat;
 const fetch=async(input,init)=>{
  captured=findText(JSON.parse(await new Request(input,init).text()));
  let reply='data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n';
  if(api==='openai-responses')reply='data: {"type":"response.completed","response":{"status":"completed"}}\n\n';
  if(api==='anthropic-messages')reply='event: message_start\ndata: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":1}}}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n';
  if(api==='google-generative-ai')reply='data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}\n\n';
  return new Response(reply,{headers:{'content-type':'text/event-stream'}});
 };
 const toolCallId=provider==='mistral'?'testtool2':'test_2';
 const text='Text with unpaired surrogate: '+String.fromCharCode(0xd83d)+' <- should be sanitized';
 const request={systemPrompt:'You are a helpful assistant.',tools:[{name:'test_tool',description:'A test tool',parameters:{type:'object',properties:{}}}],messages:[
  {role:'user',content:'Use the test tool',timestamp:1},
  {role:'assistant',content:[{type:'toolCall',id:toolCallId,name:'test_tool',arguments:{}}],api,provider,model:id,stopReason:'toolUse',timestamp:2,usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}}},
  {role:'toolResult',toolCallId,toolName:'test_tool',content:[{type:'text',text}],isError:false,timestamp:3},
  {role:'user',content:'What did the tool return?',timestamp:4}
 ]};
 const originalFetch=globalThis.fetch;
 globalThis.fetch=fetch;
 let result;
 try { result=await stream(model,request,{apiKey:'test',fetch}).result(); }
 finally { globalThis.fetch=originalFetch; }
 if(result.stopReason==='error') throw new Error(result.errorMessage);
 if(request.messages[2].content[0].text!==text)throw new Error('provider mutated history');
 rows.push([api,captured,result.stopReason]);
}
console.log(JSON.stringify(rows));
