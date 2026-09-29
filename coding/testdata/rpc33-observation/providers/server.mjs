// Multi-API RPC33 fixture: the first request gets one read tool call, the tool-result request gets text. Headers and body go out in one write, as the RPC33 Go fixture does.
import { createServer } from 'node:http';
import { readToolCall, textReply } from './bedrock-converse-stream/frames.mjs';
const args = '{"path":"parity-read-target.txt"}';
const sse = (evs) => evs.map(e => (e.event ? `event: ${e.event}\n` : '') + `data: ${typeof e.data === 'string' ? e.data : JSON.stringify(e.data)}\n\n`).join('');
function body(api) {
  switch (api) {
  case 'openai-completions': case 'mistral-conversations':
    return sse([{data:{id:'chatcmpl-strict',model:'strict',choices:[{index:0,delta:{tool_calls:[{index:0,id:'strictread1',type:'function',function:{name:'read',arguments:args}}]},finish_reason:'tool_calls'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}}},{data:'[DONE]'}]);
  case 'openai-responses': case 'azure-openai-responses': case 'openai-codex-responses': {
    const item={type:'function_call',id:'fc_1',call_id:'call_1',name:'read',arguments:''}; const fin={...item,arguments:args,status:'completed'};
    return sse([{event:'response.created',data:{type:'response.created',response:{id:'resp_1'}}},{event:'response.output_item.added',data:{type:'response.output_item.added',output_index:0,item}},{event:'response.function_call_arguments.delta',data:{type:'response.function_call_arguments.delta',output_index:0,delta:args}},{event:'response.output_item.done',data:{type:'response.output_item.done',output_index:0,item:fin}},{event:'response.completed',data:{type:'response.completed',response:{id:'resp_1',status:'completed',output:[fin],usage:{input_tokens:10,output_tokens:3,total_tokens:13}}}}]);
  }
  case 'anthropic-messages':
    return sse([
      {event:'message_start',data:{type:'message_start',message:{id:'msg_1',type:'message',role:'assistant',model:'strict',content:[],stop_reason:null,usage:{input_tokens:10,output_tokens:1}}}},
      {event:'content_block_start',data:{type:'content_block_start',index:0,content_block:{type:'tool_use',id:'toolu_1',name:'read',input:{}}}},
      {event:'content_block_delta',data:{type:'content_block_delta',index:0,delta:{type:'input_json_delta',partial_json:args}}},
      {event:'content_block_stop',data:{type:'content_block_stop',index:0}},
      {event:'message_delta',data:{type:'message_delta',delta:{stop_reason:'tool_use'},usage:{output_tokens:3}}},
      {event:'message_stop',data:{type:'message_stop'}}]);
  case 'pi-messages': {
    const toolCall = {type:'toolCall',id:'strictread1',name:'read',arguments:{path:'parity-read-target.txt'}};
    const usage = {input:10,output:3,cacheRead:0,cacheWrite:0,totalTokens:13,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}};
    return sse([{data:{type:'start'}},{data:{type:'toolcall_start',contentIndex:0,id:'strictread1',toolName:'read'}},{data:{type:'toolcall_delta',contentIndex:0,delta:args}},{data:{type:'toolcall_end',contentIndex:0,toolCall}},{data:{type:'done',reason:'toolUse',usage,responseId:'msg-strict'}}]);
  }
  case 'bedrock-converse-stream':
    return readToolCall();
  case 'google-generative-ai':
    return sse([{data:{candidates:[{content:{role:'model',parts:[{functionCall:{name:'read',args:{path:'parity-read-target.txt'}}}]},finishReason:'STOP'}],usageMetadata:{promptTokenCount:10,candidatesTokenCount:3,totalTokenCount:13},modelVersion:'strict',responseId:'g1'}}]);
  }
}
const api = process.argv[2];
let requests = 0;
const server = createServer(async (req, res) => {
  let raw = ''; for await (const c of req) raw += c;
  // The follow-up request carries the read tool result; answer it with text so the run settles.
  // Codex compresses its request body (zstd), so the tool result cannot be found in it: alternate instead.
  const second = api === 'openai-codex-responses' ? ++requests % 2 === 0 : /"role":"tool"|tool_result|functionResponse|function_call_output|toolResult/.test(raw);
  let b = body(api);
  if (second) {
    // After the tool result, reply with text so the run ends.
    if (api === 'openai-completions' || api === 'mistral-conversations') b = sse([{data:{id:'c2',model:'strict',choices:[{index:0,delta:{content:'ok'},finish_reason:'stop'}]}},{data:'[DONE]'}]);
    else if (api === 'openai-responses' || api === 'azure-openai-responses' || api === 'openai-codex-responses') { const it={type:'message',id:'m2',role:'assistant',status:'completed',content:[{type:'output_text',text:'ok',annotations:[]}]}; b = sse([{event:'response.created',data:{type:'response.created',response:{id:'r2'}}},{event:'response.output_item.added',data:{type:'response.output_item.added',output_index:0,item:{...it,content:[]}}},{event:'response.output_text.delta',data:{type:'response.output_text.delta',output_index:0,content_index:0,delta:'ok'}},{event:'response.output_item.done',data:{type:'response.output_item.done',output_index:0,item:it}},{event:'response.completed',data:{type:'response.completed',response:{id:'r2',status:'completed',output:[it]}}}]); }
    else if (api === 'anthropic-messages') b = sse([{event:'message_start',data:{type:'message_start',message:{id:'m2',type:'message',role:'assistant',model:'strict',content:[],usage:{input_tokens:1,output_tokens:1}}}},{event:'content_block_start',data:{type:'content_block_start',index:0,content_block:{type:'text',text:''}}},{event:'content_block_delta',data:{type:'content_block_delta',index:0,delta:{type:'text_delta',text:'ok'}}},{event:'content_block_stop',data:{type:'content_block_stop',index:0}},{event:'message_delta',data:{type:'message_delta',delta:{stop_reason:'end_turn'},usage:{output_tokens:1}}},{event:'message_stop',data:{type:'message_stop'}}]);
    else if (api === 'bedrock-converse-stream') b = textReply();
    else if (api === 'pi-messages') b = sse([{data:{type:'start'}},{data:{type:'text_start',contentIndex:0}},{data:{type:'text_delta',contentIndex:0,delta:'ok'}},{data:{type:'text_end',contentIndex:0,content:'ok'}},{data:{type:'done',reason:'stop',usage:{input:1,output:1,cacheRead:0,cacheWrite:0,totalTokens:2,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},responseId:'msg-reply'}}]);
    else b = sse([{data:{candidates:[{content:{role:'model',parts:[{text:'ok'}]},finishReason:'STOP'}]}}]);
  }
  const buf = Buffer.from(b);
  res.writeHead(200, {'content-type':api === 'bedrock-converse-stream' ? 'application/vnd.amazon.eventstream' : 'text/event-stream','content-length':buf.length});
  res.end(buf);
});
server.listen(0, '127.0.0.1', () => { console.log(server.address().port); });
