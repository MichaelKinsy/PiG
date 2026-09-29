import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
if (process.argv[2] === 'pig') {
  const run=spawnSync('go',['test','./ai','-run','^TestFileModelsStoreUpstream$/cancels_a_catalog_write','-count=1','-v'],{encoding:'utf8'});
  if(run.status!==0)throw new Error(run.stdout+run.stderr);
  const rows=run.stdout.split('\n').filter(line=>line.startsWith('MODELS-STORE:'));
  if(rows.length!==1)throw new Error('expected one persisted catalog');
  console.log(rows[0].slice('MODELS-STORE:'.length));
} else {
  const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
  const {FileModelsStore}=await import(pathToFileURL(join(root,'dist/core/models-store.js')).href);
  const require=createRequire(join(root,'package.json'));
  const lockfile=require('proper-lockfile');
  const dir=mkdtempSync(join(tmpdir(),'catalog-store-'));
  const path=join(dir,'models-store.json');
  const model=(provider,id)=>({id,name:id,api:'openai-completions',provider,baseUrl:'https://example.test/v1',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:1000,maxTokens:100});
  function sorted(value){if(Array.isArray(value))return value.map(sorted);if(value&&typeof value==='object')return Object.fromEntries(Object.keys(value).sort().map(key=>[key,sorted(value[key])]));return value;}
  writeFileSync(path,JSON.stringify(sorted({one:{models:[model('one','existing')]}})));
  let release;
  try {
    const store=new FileModelsStore(path);
    release=await lockfile.lock(path,{realpath:false});
    const controller=new AbortController();
    const pending=store.write('two',{models:[model('two','cancelled')]},{signal:controller.signal});
    await new Promise(resolve=>setTimeout(resolve,10));
    controller.abort();
    try{await pending;throw new Error('cancelled write succeeded');}catch(error){if(error.name!=='AbortError')throw error;}
    await release();release=undefined;
    await new Promise(resolve=>setTimeout(resolve,150));
    console.log(readFileSync(path,'utf8'));
  } finally {if(release)await release();rmSync(dir,{recursive:true,force:true});}
}
