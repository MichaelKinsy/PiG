// Pi 0.87.1 actual main-screen renderer; fixture terminal operations match tui-render.test.ts's BoundedWriteTerminal.
import assert from 'node:assert/strict';
import { readFileSync, realpathSync, mkdtempSync } from 'node:fs';
import { join, basename } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? realpathSync('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version,'0.87.1');
const dist=join(root,'node_modules/@earendil-works/pi-tui/dist');
const {TuiMainScreen}=await import(pathToFileURL(join(dist,'tui-main-screen.js')).href);
const {setCapabilities}=await import(pathToFileURL(join(dist,'terminal-image.js')).href);
setCapabilities({images:null,trueColor:false,hyperlinks:false});
function terminal(height){return {columns:40,rows:height,writes:[],start(fn){this.receive=fn},stop(){},write(data){this.writes.push(data)},hideCursor(){},showCursor(){}}}
function component(lines){return {lines,renders:0,render(){this.renders++;return this.lines},invalidate(){},handleInput(data){this.lines=[data]}}}
const large='\x1b_Ga=T,f=100;'+'A'.repeat(1200000)+'\x1b\\';
const image='\x1b_Ga=T,f=100,q=2,C=1,c=3,r=3,i=89;AAAA\x1b\\';
const cases=[
 ['full-large',24,null,[large,large],false],
 ['differential-large',24,['before'],['before',large,large],false],
 ['reserve-before-full-placement',5,['l0','l1','l2','l3','l4'],['l0','l1','l2','l3','l4',image,'','','after'],false],
 ['image-reserved-growth',5,[image,''],[image,'',''],false],
 ['cleanup-without-advertised-support',10,[image],['plain'],true],
 ['stable-deletion-order',10,['\x1b_Ga=T,i=9;A\x1b\\','\x1b_Ga=T,i=2;B\x1b\\','\x1b_Ga=T,i=9;A\x1b\\'],['plain'],true],
];
for(const [name,height,before,after,force] of cases){
 const term=terminal(height),ui=new TuiMainScreen(term),c=component(before);ui.addChild(c);
 if(before!==null){ui.renderNow();term.writes=[]}
 c.lines=after;ui.renderNow(force);
 console.log(JSON.stringify({name,output:term.writes.join(''),writes:term.writes.map(s=>s.length)}));ui.stop();
}
const term=terminal(10),ui=new TuiMainScreen(term),c=component(['initial']);ui.addChild(c);ui.setFocus(c);ui.start();ui.renderNow();
const beforeCount=c.renders;c.lines=['pending'];ui.requestRender();let requests=0;
const request=ui.requestImmediateRender.bind(ui);ui.requestImmediateRender=()=>{requests++;request()};
for(const key of ['first','second','typed'])term.receive(key);
await new Promise(resolve=>process.nextTick(resolve));
request();ui.stop();await new Promise(resolve=>process.nextTick(resolve));
console.log(JSON.stringify({name:'keyboard',renders:c.renders-beforeCount,lines:c.lines}));
const temp=mkdtempSync(join(process.argv[2],'render-')),logDir=join(temp,'logs');process.env.PI_TUI_DEBUG_REDRAW='1';
const logged=new TuiMainScreen(terminal(10),undefined,logDir);logged.addChild(component(['test']));logged.renderNow();
const log=readFileSync(join(logDir,'pi-tui-debug.log'),'utf8');console.log(JSON.stringify({Name:'debug',File:'pi-tui-debug.log',Reason:log.slice(log.indexOf('] ')+2)}));logged.stop();
process.env.PI_TUI_DEBUG_REDRAW='';for(const key of ['TMPDIR','TEMP','TMP'])process.env[key]=temp;
const crashed=new TuiMainScreen(terminal(10)),bad=component(['ok']);crashed.addChild(bad);crashed.renderNow();bad.lines=['ok','x'.repeat(60)];
let failure;try{crashed.renderNow()}catch(error){failure=error}assert.ok(failure);
const path=join(temp,'pi-tui-crash.log'),data=readFileSync(path,'utf8');
console.log(JSON.stringify({Name:'crash',File:basename(path),Referenced:failure.message.includes(path),WidthRecorded:data.includes('Terminal width: 40')}));
console.log(JSON.stringify({name:'keyboard-caller',requests}));
