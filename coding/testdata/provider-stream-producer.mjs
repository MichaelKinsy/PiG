export default function(pi){
 pi.registerProvider('extension-provider',{api:'issue-8964-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model,context,options)=>{
  if(options.apiKey!=='extension-key')throw Error('wrong key: '+options.apiKey);
  const lastContent=context.messages?.at(-1)?.content;
  if(lastContent==='cancel'||(Array.isArray(lastContent)&&lastContent[0]?.text==='cancel')){
   return {async *[Symbol.asyncIterator](){yield {type:'start',partial:{role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[],stopReason:'stop',usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},timestamp:1}};await new Promise((_,reject)=>{const abort=()=>reject(Error('provider stream aborted'));options.signal.addEventListener('abort',abort,{once:true});if(options.signal.aborted)abort();});},result:async()=>{throw Error('aborted')}};
  }
  const message={role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[{type:'text',text:'custom provider response'}],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:1};
  return {async *[Symbol.asyncIterator](){yield {type:'start',partial:message};yield {type:'text_start',contentIndex:0,partial:message};yield {type:'text_delta',contentIndex:0,delta:'custom provider response',partial:message};yield {type:'text_end',contentIndex:0,content:'custom provider response',partial:message};yield {type:'done',reason:'stop',message};},result:async()=>message};
 }});
}
