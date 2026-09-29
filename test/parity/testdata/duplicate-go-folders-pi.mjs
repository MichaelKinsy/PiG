import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, relative } from 'node:path';
import { loadExtensions } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/extensions/loader.js';
import { DefaultResourceLoader } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/resource-loader.js';
const root = await mkdtemp(join(tmpdir(), 'duplicate-folders-pi-'));
try {
 const paths=[];
 for (let i=0;i<2;i++) {
  const dir=join(root,`copy-${i}`,'ask'); await mkdir(dir,{recursive:true}); const path=join(dir,'index.mjs'); paths.push(path);
  await writeFile(path,`export default pi => { let calls=0; pi.registerCommand('ask',{description:'copy-${i}',handler: async () => {throw new Error('copy-${i}:'+ ++calls)}}); pi.registerTool({name:'shared',label:'shared',description:'shared',parameters:{type:'object'},execute:async()=>({content:[]})}); };`);
 }
 for (const phase of ['load','reload']) {
  const result=await loadExtensions(paths,root);
  if(result.errors.length)throw new Error(JSON.stringify(result.errors));
  const calls=[];
  for (const ext of result.extensions) { try {await ext.commands.get('ask').handler('',{});} catch(error){calls.push(error.message);} }
  const conflicts=DefaultResourceLoader.prototype.detectExtensionConflicts(result.extensions).map(c=>({path:relative(root,c.path).replace('/index.mjs',''),message:c.message.replace(root+'/', '').replace('/index.mjs','')}));
  console.log(JSON.stringify({phase,calls,conflicts}));
 }
} finally {await rm(root,{recursive:true,force:true});}
