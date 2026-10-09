import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.1.0") throw new Error("Expected Pi 1.1.0");
const { complete, completeSimple, getModel } = await import(new URL("dist/compat.js", root));
const cases = JSON.parse(readFileSync(new URL("./completions-tool-choice.json", import.meta.url), "utf8"));
const tools = [{name:"test_tool", description:"Test tool", parameters:{type:"object",properties:{required:{type:"string"},optional:{type:"number"}},required:["required"]}}];
for (const test of cases) {
  let body;
  const server = createServer(async (req,res) => {
    const chunks=[]; for await (const chunk of req) chunks.push(chunk);
    body=JSON.parse(Buffer.concat(chunks));
    res.writeHead(200,{"content-type":"text/event-stream"});
    res.end('data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\n');
  });
  await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
  try {
    const model={...getModel(test.provider,test.model),api:"openai-completions",baseUrl:`http://127.0.0.1:${server.address().port}`};
    if(test.strip) model.compat=undefined;
    const context={systemPrompt:"Follow instructions.",messages:[{role:"user",content:"Hi",timestamp:0}],tools:test.tools?tools:undefined};
    const options={apiKey:"test",toolChoice:test.choice,reasoning:test.thinking,reasoningEffort:test.raw};
    if(test.strict) context.tools = tools.map(tool=>({...tool,constrainedSampling:{type:"json_schema",strict:"prefer"}}));
    const result=await (test.raw?complete:completeSimple)(model,context,options);
    if(result.stopReason==="error") throw new Error(result.errorMessage);
    console.log(JSON.stringify({case:test.name,choice:body.tool_choice??null,toolStream:body.tool_stream??null,thinking:body.thinking?{clear_thinking:body.thinking.clear_thinking,type:body.thinking.type}:null,effort:body.reasoning_effort??null,reasoning:body.reasoning??null,role:body.messages[0].role,required:body.tools?.[0]?.function?.parameters?.required??null,strict:body.tools?.[0]?.function?.strict??null}));
  } finally {await new Promise(resolve=>server.close(resolve));}
}
