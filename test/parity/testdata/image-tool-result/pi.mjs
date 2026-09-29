import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const { getModel, normalizeContext } = await import(pathToFileURL(join(ai, 'compat.js')));
const image = readFileSync('ai/testdata/upstream-red-circle.png').toString('base64');
const results = {};
for (const [api, provider, id] of [['google-generative-ai','google','gemini-2.5-flash'],['openai-completions','openrouter','z-ai/glm-4.5v'],['mistral-conversations','mistral','pixtral-12b'],['openai-completions','openai','gpt-4o-mini'],['openai-responses','openai','gpt-5-mini']]) {
 for (const mixed of [false, true]) {
  let request;
  const server = createServer((req,res) => { let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{request=JSON.parse(body);res.statusCode=400;res.end('{"error":{"message":"fixture rejection"}}');}); });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  try {
   const model={...getModel(provider,id),api,baseUrl:`http://127.0.0.1:${server.address().port}`};
   const content=[];if(mixed)content.push({type:'text',text:'diameter of 100 pixels'});content.push({type:'image',data:image,mimeType:'image/png'});
   const {stream}=await import(pathToFileURL(join(ai,`api/${api}.js`)));
   await stream(model,normalizeContext({messages:[{role:'assistant',api,provider,model:id,stopReason:'toolUse',timestamp:0,usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},content:[{type:'toolCall',id:'imagecall',name:'get_circle',arguments:{}}]},{role:'toolResult',toolCallId:'imagecall',toolName:'get_circle',content,isError:false,timestamp:0}]}),{apiKey:'test',maxRetries:0}).result();
   if(!request)throw new Error('no provider request');
   const images=[];
   const visit=value=>{if(Array.isArray(value)){value.forEach(visit);return;}if(!value||typeof value!=='object')return;
    if(value.inlineData)images.push(`data:${value.inlineData.mimeType};base64,${value.inlineData.data}`);
    if(typeof value.image_url==='string')images.push(value.image_url);else if(value.image_url?.url)images.push(value.image_url.url);
    Object.keys(value).sort().forEach(key=>visit(value[key]));
   };
   visit(request);results[`${provider}/${api}/${mixed}`]={hasDescription:JSON.stringify(request).includes('diameter of 100 pixels'),images};
  }finally{await new Promise(resolve=>server.close(resolve));}
 }
}
console.log(JSON.stringify(Object.fromEntries(Object.keys(results).sort().map(key=>[key,results[key]]))));
