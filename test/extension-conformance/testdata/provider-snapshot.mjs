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
}
