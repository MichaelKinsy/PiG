import ts from '../../../extensions/sdk-ts/node_modules/typescript/lib/typescript.js';
import {getModel} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {resolve,dirname} from 'node:path';
import vm from 'node:vm';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'../../..');
const path=resolve(root,'.upstream/v0.87.1/packages/ai/test/stream.test.ts');
const source=readFileSync(path,'utf8');
const file=ts.createSourceFile(path,source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const helpers=new Set(['basicTextGeneration','handleToolCall','handleStreaming','handleThinking','handleImage','multiTurn']);
const lines=[];
function calleeName(node){while(ts.isCallExpression(node)||ts.isPropertyAccessExpression(node))node=node.expression;return ts.isIdentifier(node)?node.text:'';}
function collect(node){if(ts.isCallExpression(node)&&calleeName(node.expression)==='it'&&ts.isStringLiteral(node.arguments[0]))lines.push(file.getLineAndCharacterOfPosition(node.getStart()).line+1);ts.forEachChild(node,collect);}
collect(file);
const transformed=ts.transform(file,[context=>{
 const visit=node=>{
  if(ts.isFunctionDeclaration(node)&&helpers.has(node.name?.text))return ts.factory.updateFunctionDeclaration(node,node.modifiers,node.asteriskToken,node.name,node.typeParameters,node.parameters,node.type,ts.factory.createBlock([ts.factory.createReturnStatement(ts.factory.createCallExpression(ts.factory.createPropertyAccessExpression(ts.factory.createIdentifier('globalThis'),'recordHelper'),undefined,[ts.factory.createStringLiteral(node.name.text),...node.parameters.map(p=>ts.factory.createIdentifier(p.name.text))]))],true));
  return ts.visitEachChild(node,visit,context);
 };
 return node=>ts.visitNode(node,visit);
}]);
let text=ts.createPrinter().printFile(transformed.transformed[0]).replaceAll('import.meta.url',JSON.stringify(pathToFileURL(path).href));
text=ts.transpileModule(text,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const cases=[],scopes=[];let current;
const describe=(name,fn)=>{const scope={name,before:[],after:[]};scopes.push(scope);fn();scopes.pop();};describe.skipIf=()=>describe;
const it=(name,...args)=>cases.push({name,fn:args.find(v=>typeof v==='function'),scopes:[...scopes],line:lines[cases.length]});it.skipIf=()=>it;it.skip=it;
const expect=new Proxy(()=>expect,{get:()=>expect,apply:()=>expect});
const env={};for(const m of source.matchAll(/process\.env\.([A-Z0-9_]+)/g))env[m[1]]='fixture-'+m[1];env.PI_NO_LOCAL_LLM='';
const output=[];
const record=(kind,model,options,request)=>{
 output.push({source:'.upstream/v0.87.1/packages/ai/test/stream.test.ts:'+current.line,line:current.line,name:current.scopes.map(s=>s.name).concat(current.name).join('/'),kind,provider:model.provider,model:model.id,api:model.api,hasCompat:model.compat!==undefined,options:options??{},...(model.provider==='ollama'?{customModel:model}:{}),...(request?{request}:{})});
};
const requireNative=createRequire(import.meta.url);
const sandbox={Date:class extends Date {static now(){return 1;}},console:{log(){},warn(){}},process:{env,cwd:()=>root},Buffer,URL,queueMicrotask,setTimeout:fn=>{queueMicrotask(fn);return 0;},fetch:async()=>({ok:true}),recordHelper:record};
sandbox.globalThis=sandbox;
const require=specifier=>{
 if(specifier==='vitest')return{describe,it,expect,beforeAll:fn=>scopes.at(-1).before.push(fn),afterAll:fn=>scopes.at(-1).after.push(fn)};
 if(specifier==='child_process')return{execSync:()=>'',spawn:()=>({kill(){}})};
 if(specifier==='typebox')return{Type:{Object:properties=>({type:'object',properties,required:Object.keys(properties)}),Number:options=>({type:'number',...options})}};
 if(specifier.includes('typebox-helpers'))return{StringEnum:(values,options)=>({type:'string',enum:values,...options})};
 if(specifier.includes('compat.ts'))return{getModel,stream(){throw new Error('unexpected raw stream in extractor');},complete:async(model,request,options)=>{record('bedrockSpecial',model,options,request);options?.onPayload?.({additionalModelRequestFields:{thinking:{type:'adaptive',display:'summarized'},output_config:{effort:'max'}},...(options.requestMetadata?{requestMetadata:options.requestMetadata}:{})});return{stopReason:'stop'};}};
 if(specifier.includes('azure-utils'))return{hasAzureOpenAICredentials:()=>true,resolveAzureDeploymentName:()=>undefined};
 if(specifier.includes('bedrock-utils'))return{hasBedrockCredentials:()=>true};
 if(specifier.includes('cloudflare-utils'))return{hasCloudflareAiGatewayCredentials:()=>true,hasCloudflareWorkersAICredentials:()=>true};
 if(specifier.includes('oauth.ts'))return{resolveApiKey:async id=>'oauth:'+id};
 if(specifier==='@google/genai')return{ThinkingLevel:{LOW:'LOW',MEDIUM:'MEDIUM'}};
 return requireNative(specifier);
};
await vm.runInNewContext('(async function(require,module,exports){'+text+'\n})',sandbox)(require,{exports:{}},{});
const ran=new Set();
for(const item of cases){current=item;for(const scope of item.scopes)for(const fn of scope.before)if(!ran.has(fn)){ran.add(fn);await fn();}await item.fn();}
console.log(JSON.stringify(output,null,2));
