import {join,resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const require=createRequire(join(root,'package.json'));
const undici=require('undici');
const util=require('undici/lib/core/util.js');
const setup=util.setupConnectTimeout;
const observed=[];
// Observe the real dispatcher-selected connector timeout without changing its timer or callback.
util.setupConnectTimeout=(socket,options)=>{observed.push(options.timeout);return setup(socket,options);};
const {configureHttpDispatcher}=await import(pathToFileURL(join(root,'dist/core/http-dispatcher.js')));
const ai=join(root,'node_modules/@earendil-works/pi-ai/dist');
const {streamSimple}=await import(pathToFileURL(join(ai,'api/openai-completions.js')));
const {normalizeContext}=await import(pathToFileURL(join(ai,'compat.js')));
configureHttpDispatcher(100);
try{
 const model={id:'gpt-test',name:'GPT Test',provider:'openai',api:'openai-completions',baseUrl:process.argv[2],reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096};
 const result=await streamSimple(model,normalizeContext({messages:[{role:'user',content:'hi',timestamp:0}]}),{apiKey:'test'}).result();
 if(result.stopReason==='error')throw new Error(result.errorMessage);
 if(!observed.length||observed.some(timeout=>timeout!==10000))throw new Error(`Unexpected pinned connector defaults: ${JSON.stringify(observed)}`);
 console.log(JSON.stringify({reason:result.stopReason,text:result.content.filter(block=>block.type==='text').map(block=>block.text).join('')}));
}finally{util.setupConnectTimeout=setup;await undici.getGlobalDispatcher().close();}
