import {getApiProvider} from '@earendil-works/pi-ai/compat';
export default function(pi){
 pi.registerCommand('stream-custom',{description:'Custom provider regression',handler:async(method,ctx)=>{
  const proof=method.endsWith(':proof');method=method.replace(':proof','');
  if(getApiProvider('issue-8964-extension-api')!==undefined)throw Error('custom API leaked globally before command');
  const model=ctx.modelRegistry.find('extension-provider','faux');if(!model)throw Error('registered model missing');
  const stream=ctx.modelRegistry[method](model,{messages:[{role:'user',content:'Hello',timestamp:1}]});let text='';
  for await(const event of stream){if(event.type==='text_delta')text+=event.delta;}
  const result=await stream.result();
  if(text!=='custom provider response'||result.stopReason!=='stop'||result.content[0].text!=='custom provider response')throw Error(JSON.stringify({text,result}));
  if(getApiProvider('issue-8964-extension-api')!==undefined)throw Error('custom API leaked globally after command');
  if(proof)await pi.appendEntry('provider-proof',{method,text,stopReason:result.stopReason,content:result.content,globalRegistered:false});
 }});
}
