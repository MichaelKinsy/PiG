import {readFileSync} from 'node:fs';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {join} from 'node:path';
const root=fileURLToPath(new URL('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent',import.meta.url));
if(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version!=='1.0.1')throw new Error('Expected Pi 1.0.1');
const {Editor}=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-tui/dist/components/editor.js')));
const theme={borderColor:s=>s,selectList:{selectedPrefix:s=>s,selectedText:s=>s,description:s=>s,scrollInfo:s=>s,noMatch:s=>s}};
const tui={terminal:{columns:80,rows:24},requestRender(){}};
const hex=s=>Buffer.from(s).toString('hex');
const paste='\x1b[200~'+'line\n'.repeat(20).trimEnd()+'\x1b[201~';
const results=[];
for(const [name,text,keys] of [
 ['mixed CJK','hello你好，world世界',['\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x1b[1;5C','\x1b[1;5C','\x1b[1;5C','\x1b[1;5C']],
 ['SEA Thai','ภาษาไทยภาษาไทย',['\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19','\x01','\x1bd']],
 ['SEA Lao','ສະບາຍດີ ພາສາລາວ!',['\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19','\x01','\x1bd']],
 ['SEA Khmer','សួស្តីពិភពលោក ភាសាខ្មែរ',['\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19','\x01','\x1bd']],
 ['SEA Burmese','မြန်မာဘာသာစကား,မင်္ဂလာပါ',['\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19','\x01','\x1bd']],
 ['SEA mixed marker','ภาษาไทย',[paste,'ພາສາລາວ မြန်မာဘာသာစကား','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19']],
 ['word deletion','学生です，hello.foo 😀😀',['\x17','\x17','\x17','\x17','\x17','\x19']],
 ['marker arrows and backspace','A',[paste,'B','\x1b[D','\x1b[D','\x1b[C','\x7f']],
 ['marker forward delete','A',[paste,'B','\x01','\x1b[C','\x1b[3~']],
 ['marker word movement','学生 ',[paste,' 世界','\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x17','\x19']],
 ['multiple markers','',[paste,' ',paste,'\x01','\x1b[C','\x1b[C','\x1b[C']],
 ['unregistered marker','[paste #99 +5 lines]',['\x01','\x1b[C','\x1b[1;5C','\x1b[3~']],
 ['marker mouse','A',[paste,'B','\x01','click inside marker']],
 ['marker beside unregistered text','',[paste,' [paste #99 +5 lines]','\x01','\x1b[C','\x1b[C','\x1b[C']],
 ['history backward delete','',['\x1b[A','\x17','\x1b[B']],
 ['history forward delete','',['\x1b[A','\x1bd','\x1b[A']],
 ['kill chain','first second',['\x17','\x1b[1;5D','\x1b[1;5C','\x17','\x19']],
 ['sticky reset','abcdefghij\nword\nabcdefghij',['\x01','\x1b[C','\x1b[C','\x1b[C','\x1b[C','\x1b[C','\x1b[C','\x1b[C','\x1b[C','\x1b[A','\x1b[1;5D','\x1b[1;5C','\x1b[B']],
 ['line boundaries','one\ntwo',['\x1b[1;5C','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5D','\x1b[1;5C','\x1b[1;5C','\x1bd']],
]) {
 const e=new Editor(tui,theme);e.setText(text);
 if(name.startsWith('history ')){e.addToHistory('older');e.addToHistory('newer');}
 const states=[];
 const capture=()=>states.push({text:hex(e.getText()),expanded:hex(e.getExpandedText()),cursor:e.getCursor(),rows:e.render(80).slice(1,-1).map(hex)});
 capture();for(const key of keys){
  if(key==='click inside marker') e.handleMouse({type:'click',button:'left',x:10,y:1,width:80});
  else e.handleInput(key);
  capture();
 }
 results.push({name,states});
}
console.log(JSON.stringify(results));
