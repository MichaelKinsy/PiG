// Replay the real Powerline and pi-acp directory contracts in isolated homes.
// Usage: node test/parity/testdata/pi-directories-corpus.mjs <pig> <pi> <powerline-npm-root> <pi-acp-index.js> <output-dir>
import assert from 'node:assert/strict';
import {spawn, spawnSync, execFileSync} from 'node:child_process';
import {createInterface} from 'node:readline';
import {mkdirSync, writeFileSync, readFileSync, appendFileSync, cpSync, renameSync, existsSync, mkdtempSync} from 'node:fs';
import {join, resolve, dirname} from 'node:path';
import {tmpdir} from 'node:os';

const [pig, pi, powerlineNpm, adapter, output] = process.argv.slice(2).map(p => resolve(p));
const root = resolve(import.meta.dirname, '../../..');
const scratch = mkdtempSync(join(tmpdir(), 'parity-pi-dirs-'));
mkdirSync(output, {recursive:true});
writeFileSync(join(output,'scratch.txt'),scratch+'\n');
const recentRows = new Map();
const settings = {defaultProjectTrust:'always',defaultProvider:'test-faux',defaultModel:'faux-1',enableInstallTelemetry:false,lastChangelogVersion:'0.87.1',compaction:{enabled:false},retry:{enabled:false}};
const model = {id:'faux-1',name:'Test Faux',api:'test-faux',reasoning:false,input:['text','image'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096};
function setup(label, binary, isPi, quietStartup) {
  const home = join(scratch,label), cwd = join(home,'work'), agent = join(home,'.pi/agent');
  mkdirSync(cwd,{recursive:true}); mkdirSync(join(agent,'prompts'),{recursive:true});
  writeFileSync(join(agent,'settings.json'),JSON.stringify({...settings,quietStartup}));
  writeFileSync(join(agent,'models.json'),JSON.stringify({providers:{'test-faux':{baseUrl:'http://localhost:0',api:'test-faux',apiKey:'unused',models:[model]}}}));
  if(isPi) {
    mkdirSync(join(agent,'extensions'),{recursive:true});
    cpSync(join(root,'test/parity/testdata/test-faux-provider.ts'),join(agent,'extensions/faux.ts'));
  }
  const env = {HOME:home,USERPROFILE:home,PATH:dirname(process.execPath)+':'+process.env.PATH,LANG:'C.UTF-8',TERM:'xterm-256color',COLORTERM:'truecolor',PI_OFFLINE:'1',PIG_OFFLINE:'1',PI_TELEMETRY:'0',PIG_USE_PI_DIRS:'1',PIG_TEST_FAUX:'1',PIG_TEST_FAUX_SCENARIO:'parity-basic',PI_ACP_PI_COMMAND:binary};
  return {home,cwd,agent,env,binary,label};
}
function print(s, args) {
  const p = spawnSync(s.binary,['--model','test-faux/faux-1',...args],{cwd:s.cwd,env:s.env,encoding:'utf8',timeout:30000});
  writeFileSync(join(output,s.label+'-print.txt'),p.stdout+'\nSTDERR\n'+p.stderr);
  assert.equal(p.status,0,p.stderr); return p.stdout;
}
const quote = s => "'"+s.replaceAll("'","'\\''")+"'";
async function welcome(s) {
  const recent = join(s.home,'recent-session'); mkdirSync(recent);
  const work = s.cwd; s.cwd=recent;
  assert.equal(print(s,['-p','reply with exactly: RECENT']), 'RECENT\n');
  s.cwd=work;
  cpSync(powerlineNpm,join(s.agent,'npm'),{recursive:true});
  writeFileSync(join(s.agent,'settings.json'),JSON.stringify({...settings,quietStartup:false,packages:['npm:pi-powerline-footer']}));
  const session = 'parity-pi-dirs-'+process.pid+'-'+s.label;
  const sock = 'parity-pi-dirs-'+process.pid;
  const tmux = (...args) => execFileSync('tmux',['-L',sock,...args],{encoding:'utf8'});
  const command = 'cd '+quote(s.cwd)+' && exec env -i '+Object.entries(s.env).map(([k,v])=>quote(k+'='+v)).join(' ')+' '+quote(s.binary)+' --model test-faux/faux-1 2>'+quote(join(output,s.label+'-welcome.stderr'));
  tmux('new-session','-d','-s',session,'-x','130','-y','42',command);
  try {
    const deadline = Date.now()+30000;
    let pane='';
    do {
      pane=tmux('capture-pane','-p','-J','-t',session);
      if(pane.includes('recent-session')) break;
      await new Promise(r=>setTimeout(r,100));
    } while(Date.now()<deadline);
    writeFileSync(join(output,s.label+'-welcome.txt'),pane);
    const ansi=tmux('capture-pane','-e','-p','-t',session);
    writeFileSync(join(output,s.label+'-welcome.ansi'),ansi);
    recentRows.set(s.label,ansi.split('\n').find(line=>line.includes('recent-session')));
    assert(pane.includes('recent-session'),s.label+' welcome lost the real print session');
    assert(pane.includes('just now'),s.label+' welcome has no recent time');
  } finally { tmux('kill-session','-t',session); }
}
async function acp(s) {
  mkdirSync(join(s.cwd,'.pi/prompts'),{recursive:true});
  mkdirSync(join(s.cwd,'.pig/prompts'),{recursive:true});
  mkdirSync(join(s.home,'.pig/agent/prompts'),{recursive:true});
  writeFileSync(join(s.home,'.pig/agent/prompts/shared-global.md'),'---\ndescription: Wrong Pig global template\n---\nreply with exactly: WRONGGLOBAL-$1\n');
  writeFileSync(join(s.agent,'prompts/shared-global.md'),'---\ndescription: Global Pi template\n---\nreply with exactly: GLOBAL-$1\n');
  writeFileSync(join(s.cwd,'.pi/prompts/shared-project.md'),'---\ndescription: Project Pi template\n---\nreply with exactly: PROJECT-$1\n');
  writeFileSync(join(s.cwd,'.pig/prompts/shared-project.md'),'---\ndescription: Wrong Pig template\n---\nreply with exactly: WRONG-$1\n');
  const log=join(output,s.label+'-acp.jsonl');
  let proc, seq=0, sid; const pending=new Map(); let updates=[], inventories=[];
  const record=(direction,message)=>appendFileSync(log,JSON.stringify({direction,message})+'\n');
  const request=(method,params)=>new Promise((resolve,reject)=>{const id=++seq; pending.set(id,{resolve,reject}); const m={jsonrpc:'2.0',id,method,params};record('send',m);proc.stdin.write(JSON.stringify(m)+'\n');});
  function start() {
    proc=spawn(process.execPath,[adapter],{cwd:s.cwd,env:s.env,stdio:['pipe','pipe','pipe']});
    proc.stderr.on('data',d=>appendFileSync(join(output,s.label+'-acp.stderr'),d));
    createInterface({input:proc.stdout}).on('line',line=>{
      const m=JSON.parse(line);record('receive',m);
      if(m.id!==undefined&&!m.method) {const p=pending.get(m.id); if(p) {pending.delete(m.id);m.error?p.reject(new Error(JSON.stringify(m.error))):p.resolve(m.result);}}
      if(m.params?.update) {
        updates.push(m.params.update);
        if(m.params.update.sessionUpdate==='available_commands_update') inventories.push(m.params.update.availableCommands);
      }
    });
  }
  async function stop() {if(proc.exitCode!==null)return;const exited=new Promise(r=>proc.once('exit',r));proc.kill('SIGTERM');await exited;}
  const init=()=>request('initialize',{protocolVersion:1,clientInfo:{name:'pi-directory-probe',version:'1'},clientCapabilities:{}});
  const prompt=async text=>{updates=[];const result=await request('session/prompt',{sessionId:sid,prompt:[{type:'text',text}]});assert.equal(result.stopReason,'end_turn');return updates.filter(u=>u.sessionUpdate==='agent_message_chunk').map(u=>u.content?.text??'').join('');};
  const deadline=setTimeout(()=>{proc.kill('SIGTERM');throw new Error('ACP probe deadline');},30000);
  try {
    start(); await init(); sid=(await request('session/new',{cwd:s.cwd,mcpServers:[]})).sessionId;
    assert.equal(await prompt('/shared-global ARG'),'GLOBAL-ARG');
    assert.equal(await prompt('/shared-project ARG'),'PROJECT-ARG');
    assert(inventories.some(commands=>commands.some(c=>c.name==='shared-global'&&c.description==='Global Pi template')&&commands.some(c=>c.name==='shared-project'&&c.description==='Project Pi template')),'template inventory disagrees with execution');
    const listed=await request('session/list',{cwd:s.cwd});
    assert(listed.sessions.some(x=>x.sessionId===sid),'native session/list omitted current session');
    const mapPath=join(s.home,'.pi/pi-acp/session-map.json');
    assert(existsSync(mapPath),'adapter did not create its own Pi session map');
    const map=readFileSync(mapPath,'utf8');assert(map.includes('/.pi/agent/sessions/'),map);
    await stop(); start(); await init(); updates=[];
    await request('session/load',{sessionId:sid,cwd:s.cwd,mcpServers:[]});
    assert(updates.some(u=>u.sessionUpdate==='agent_message_chunk'&&u.content?.text==='GLOBAL-ARG'),'mapped load did not replay history');
    assert.equal(await prompt('reply with exactly: MAPPED-RESUME'),'MAPPED-RESUME');
    await stop(); renameSync(mapPath,mapPath+'.saved');
    start(); await init(); updates=[];
    await request('session/load',{sessionId:sid,cwd:s.cwd,mcpServers:[]});
    assert(updates.some(u=>u.sessionUpdate==='agent_message_chunk'&&u.content?.text==='PROJECT-ARG'),'native load did not replay history');
    assert.equal(await prompt('reply with exactly: NATIVE-RESUME'),'NATIVE-RESUME');
    console.log(s.label+': global/project templates, native list, mapped restart, unmapped restart PASS');
  } finally {clearTimeout(deadline);await stop();}
}
for(const [label,binary,isPi] of [['pi',pi,true],['pig',pig,false]]) {
  await welcome(setup(label+'-powerline',binary,isPi,false));
  console.log(label+': Powerline welcome recent-session / just now PASS');
  await acp(setup(label+'-adapter',binary,isPi,true));
}
assert.equal(recentRows.get('pig-powerline'),recentRows.get('pi-powerline'),'Powerline recent-session ANSI row differs');
console.log('Evidence: '+output+'; isolated homes: '+scratch);
