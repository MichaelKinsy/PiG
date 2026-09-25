// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// Run with Node 24 from a worktree with the pinned .upstream mirror.
import {createForkSnapshot} from '../../../../.upstream/current/packages/agent/src/harness/session/fork.ts';
import {writeFileSync} from 'node:fs';
const value=(namespace,key,v,seq)=>({address:{namespace,key,kind:'value'},value:v,seq});
const entry=(id,parentId,seq)=>({id,parentId,seq,timestamp:1700000000000,type:'custom',customType:id});
const base={entries:[entry('root',null,2),entry('child','root',5),entry('sibling','root',8)],scalarValues:[value('pi.session.name','','source',9),value('pi.branch.tip','main','child',10),value('pi.lane.config','main',{model:'test'},11),value('pi.lane.state','main',{inbox:[{id:'pending'}]},12),value('pi.branch.tip','other','sibling',13),value('pi.lane.config','other',{},14),value('pi.lane.state','other',{inbox:[]},15),value('pi.entry.label','root','root label',16),value('pi.entry.label','sibling','sibling label',17),value('custom.state','x',{nested:true},18),value('pi.result','work',{done:true},19)]};
const cases=[];
function add(name,options,change=()=>{}){const source=structuredClone(base);change(source);const row={name,source,options};try{const out=createForkSnapshot(source,options);row.want={...out,entries:[...out.entries.values()]};}catch(e){row.error=e.message;}cases.push(row);}
add('tree',{scope:'tree'});
add('branch',{scope:'branch',branch:'main'});
add('before child',{scope:'branch',branch:'main',entryId:'child',position:'before'});
add('at root',{scope:'branch',branch:'main',entryId:'root',position:'at'});
add('duplicate entry last value first position',{scope:'tree'},s=>s.entries.push({...s.entries[0],seq:20,customType:'replacement'}));
add('missing tip',{scope:'tree'},s=>s.scalarValues=s.scalarValues.filter(v=>v.address.namespace!=='pi.branch.tip'));
add('incomplete lane',{scope:'tree'},s=>s.scalarValues=s.scalarValues.filter(v=>!(v.address.namespace==='pi.lane.state'&&v.address.key==='main')));
add('unconfigured branch',{scope:'branch',branch:'main'},s=>s.scalarValues=s.scalarValues.filter(v=>!(v.address.namespace.startsWith('pi.lane.')&&v.address.key==='main')));
add('unknown tip',{scope:'tree'},s=>s.entries=s.entries.filter(e=>e.id!=='sibling'));
add('partial branch permits other unknown tip',{scope:'branch',branch:'main'},s=>{s.entriesComplete=false;s.entries=s.entries.filter(e=>e.id!=='sibling');});
add('partial tree rejects other unknown tip',{scope:'tree'},s=>{s.entriesComplete=false;s.entries=s.entries.filter(e=>e.id!=='sibling');});
add('unknown reserved state',{scope:'tree'},s=>s.scalarValues.push(value('pi.unknown','x',1,21)));
add('unknown source branch',{scope:'branch',branch:'absent'});
add('entry outside branch',{scope:'branch',branch:'main',entryId:'sibling'});
add('empty tree',{scope:'tree'},s=>{s.entries=[];s.scalarValues=[];});
writeFileSync(new URL('./source-fork-snapshots.json', import.meta.url),JSON.stringify(cases,null,2)+'\n');
console.log(`Generated ${cases.length} cases from pinned upstream createForkSnapshot`);
