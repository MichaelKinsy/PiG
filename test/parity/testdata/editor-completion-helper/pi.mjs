import {readFileSync} from 'node:fs';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {join} from 'node:path';
const root=fileURLToPath(new URL('../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent',import.meta.url));
if(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version!=='1.0.1')throw new Error('Expected Pi 1.0.1');
const {Editor}=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-tui/dist/components/editor.js')));
const {CombinedAutocompleteProvider}=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-tui/dist/autocomplete.js')));
// Pi 0.99.1's own editor theme (theme.ts:1135-1149, dark.json): selectedText is the dark theme's violet accent, not the
// teal of 0.87.1. The runner pins COLORTERM=truecolor for both binaries.
const {initTheme,getEditorTheme}=await import(pathToFileURL(join(root,'dist/modes/interactive/theme/theme.js')));
initTheme('dark',false);
const theme=getEditorTheme();
const tui={terminal:{columns:80,rows:24},requestRender(){}};
const hex=s=>Buffer.from(s).toString('hex');
const results=[];
for(const tc of JSON.parse(readFileSync(new URL('./cases.json',import.meta.url),'utf8'))){
 const e=new Editor(tui,theme);
 let invocation;
 const provider={
  async getSuggestions(lines,row,col,{force}){
   const before=lines[row].slice(0,col);
   let prefix=before,values=tc.values,filter=tc.filter;
   switch(tc.mode){
    case 'force': if(!force&&!prefix.includes('/')&&!prefix.startsWith('.'))return null;filter=true;break;
    case 'cursor': if(!before.startsWith('/'))return null;if(before.includes(' ')){prefix=before.slice(before.indexOf(' ')+1);values=['repo','message','help'];}else values=['cmd'];break;
    case 'slash': if(!before.startsWith('/'))return null;values=['/model','/help'];break;
    case 'argument': if(!before.includes(' ')||!before.slice(before.indexOf(' ')+1))return null;prefix=before.slice(before.indexOf(' ')+1);break;
   }
   const items=values.filter(value=>!filter||value.toLowerCase().startsWith(prefix.toLowerCase())).map(value=>({value,label:value}));
   return items.length?{prefix,items}:null;
  },
  applyCompletion(lines,row,col,item,prefix){
   const out=[...lines];out[row]=lines[row].slice(0,col-prefix.length)+item.value+lines[row].slice(col);
   return {lines:out,cursorLine:row,cursorCol:col-prefix.length+item.value.length};
  }
 };
 if(tc.mode==='awaited'||tc.mode==='invalid'){
  e.setAutocompleteProvider(new CombinedAutocompleteProvider([{name:'load-skills',description:'Load skills',getArgumentCompletions:async prefix=>{
   invocation=prefix;
   return tc.mode==='invalid'?'not-an-array':prefix.startsWith('s')?[{value:'skill-a',label:'skill-a'}]:null;
  }}],process.cwd()));
 }else{
  e.setAutocompleteProvider(tc.mode==='combined'?new CombinedAutocompleteProvider([{name:'help',description:'Show help'},{name:'model',description:'Switch model',getArgumentCompletions:()=>[{value:'claude-opus',label:'claude-opus'}]}],process.cwd()):provider);
 }
 if(tc.text)e.setText(tc.text);
 const states=[];
 for(const keys of tc.steps){
  for(const key of keys)e.handleInput(key);
  await Promise.resolve();await new Promise(resolve=>setImmediate(resolve));
  states.push({text:hex(e.getText()),cursor:e.getCursor(),open:e.isShowingAutocomplete(),menu:e.render(80).slice(3).map(hex)});
 }
 results.push({name:tc.name,states,...(invocation!==undefined?{invocation}:{})});
}
console.log(JSON.stringify(results));
