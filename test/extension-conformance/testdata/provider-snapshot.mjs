// A producer that appends to one shared partial message and pushes it. Pi's providers await between the pushes of a response, so the consumer turned each event into its wire form before the next append: an event shows the message as it was when it was pushed.
export default function(pi){
 pi.registerProvider('snapshot-provider',{api:'snapshot-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model)=>{
  const block={type:'text',text:''};
  const message={role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[block],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:1};
  return {async *[Symbol.asyncIterator](){
   yield {type:'start',partial:message};
   let text='';
   for(let i=0;i<100;i++){text+='x';block.text=text;yield {type:'text_delta',contentIndex:0,delta:'x',partial:message};}
   block.text=text+'-AFTER';
   yield {type:'done',reason:'stop',message};
  },result:async()=>message};
 }});
 // The producer answers the abort of its request by ending its stream with a result: it yields start and three text_delta events, waits until its request's signal aborts, and returns, yielding nothing more.
 pi.registerProvider('cancel-end-provider',{api:'cancel-end-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model,context,options)=>{
  const block={type:'text',text:''};
  const message={role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[block],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:1};
  return {async *[Symbol.asyncIterator](){
   yield {type:'start',partial:message};
   for(let i=0;i<3;i++){
    block.text='x'.repeat(i+1);yield {type:'text_delta',contentIndex:0,delta:'x',partial:message};
   }
   if(!options.signal.aborted)await new Promise(resolve=>options.signal.addEventListener('abort',resolve,{once:true}));
  },result:async()=>message};
 }});
 // The producer is cancelled in the middle of a burst: it yields start and three text_delta events, waits until its request's signal aborts, and goes on yielding.
 pi.registerProvider('cancel-burst-provider',{api:'cancel-burst-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model,context,options)=>{
  const block={type:'text',text:''};
  const message={role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[block],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:1};
  return {async *[Symbol.asyncIterator](){
   yield {type:'start',partial:message};
   for(let i=0;i<6;i++){
    if(i===3&&!options.signal.aborted)await new Promise(resolve=>options.signal.addEventListener('abort',resolve,{once:true}));
    block.text='x'.repeat(i+1);yield {type:'text_delta',contentIndex:0,delta:'x',partial:message};
   }
   yield {type:'done',reason:'stop',message};
  },result:async()=>message};
 }});
 // The producer answers the abort of its request with an error event that holds its partial message: it yields start and three text_delta events, waits until its request's signal aborts, and yields the error event (reason aborted) whose message is the partial with stopReason aborted and its errorMessage.
 pi.registerProvider('cancel-error-provider',{api:'cancel-error-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model,context,options)=>{
  const block={type:'text',text:''};
  const message={role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[block],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:1};
  return {async *[Symbol.asyncIterator](){
   yield {type:'start',partial:message};
   for(let i=0;i<3;i++){
    block.text='x'.repeat(i+1);yield {type:'text_delta',contentIndex:0,delta:'x',partial:message};
   }
   if(!options.signal.aborted)await new Promise(resolve=>options.signal.addEventListener('abort',resolve,{once:true}));
   message.stopReason='aborted';message.errorMessage='the author saw the abort';
   yield {type:'error',reason:'aborted',error:message};
  },result:async()=>message};
 }});
 // streamSimple waits until its request's signal aborts and fails, showing nothing.
 pi.registerProvider('cancel-throw-provider',{api:'cancel-throw-extension-api',baseUrl:'https://extension.invalid',apiKey:'extension-key',models:[{id:'faux',name:'Faux',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096}],streamSimple:async(model,context,options)=>{
  if(!options.signal.aborted)await new Promise(resolve=>options.signal.addEventListener('abort',resolve,{once:true}));
  throw new Error("the author's stream threw at the abort");
 }});
}
