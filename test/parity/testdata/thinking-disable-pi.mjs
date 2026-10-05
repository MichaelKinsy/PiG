import {readFileSync} from "node:fs";
const root=new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/",import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!=="1.0.2")throw new Error("Expected Pi 1.0.2");
const {getModel,streamSimple}=await import(new URL("dist/compat.js",root));
const cases=JSON.parse(readFileSync(new URL("thinking-disable.json",import.meta.url),"utf8"));
for(const test of cases){
 let payload;
 const result=await streamSimple(getModel(test.provider,test.model),{systemPrompt:"You are a precise assistant. Follow the requested output format exactly.",messages:[{role:"user",content:"Before replying, carefully solve 36863 * 5279 internally. Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text.",timestamp:0}]},{apiKey:"test",maxTokens:test.maxTokens,...(test.provider==="openai"?{}:{temperature:0}),onPayload:value=>{payload=value;throw new Error("payload captured")}}).result();
 if(!result.errorMessage?.includes("payload captured"))throw new Error(JSON.stringify(result));
 const control=test.provider==="anthropic"?payload.thinking:(test.provider==="google"||test.provider==="google-vertex")?payload.config.thinkingConfig:payload.reasoning;
 console.log(`${test.provider}/${test.model} ${JSON.stringify(control??null)}`);
}
