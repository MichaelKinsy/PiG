import assert from "node:assert/strict";
import {writeFileSync} from "node:fs";
export default function(pi){
 const tiers=[{inputTokensAbove:272000,input:2,output:3,cacheRead:0.2,cacheWrite:2.5}];
 pi.registerProvider("instant-provider",{baseUrl:"https://provider.test/v1",apiKey:"provider-test-key",api:"openai-completions",models:[{id:"instant-model",name:"Instant Model",reasoning:false,input:["text"],cost:{input:1,output:2,cacheRead:0.1,cacheWrite:1.25,tiers},contextWindow:128000,maxTokens:4096}]});
 pi.registerCommand("cost-tier-probe",{handler:async(path,ctx)=>{
  const model=ctx.modelRegistry.find("instant-provider","instant-model");
  assert.deepEqual(model.cost.tiers,tiers);
  writeFileSync(path,JSON.stringify(model.cost.tiers));
 }});
}
