export default function(pi) {
 const model={id:"native-model",name:"Native Model",api:"openai-completions",provider:"parity-native",baseUrl:"http://127.0.0.1:9/v1",reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:1000,maxTokens:100};
 const provider={id:"parity-native",name:"Native",baseUrl:model.baseUrl,auth:{apiKey:{name:"API key",login:async()=>({type:"api_key",key:"key"}),check:async()=>({type:"api_key",source:"native"}),resolve:async()=>({auth:{apiKey:"key"},source:"native"})}},getModels:()=>[model],stream(){throw new Error("not streamed")},streamSimple(){throw new Error("not streamed")}};
 pi.registerProvider(provider);
 pi.registerCommand("native-probe",{description:"Probe native registration",handler:async(_args,ctx)=>{
  console.log(JSON.stringify({ownerObject:ctx.modelRegistry.getRegisteredNativeProvider("parity-native")===provider,found:!!ctx.modelRegistry.find("parity-native","native-model"),catalog:ctx.modelRegistry.getAll().some(m=>m.provider==="parity-native")}));
 }});
}
