import assert from 'node:assert/strict';
import {readFileSync,realpathSync} from 'node:fs';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??realpathSync('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version,'1.0.3');
const dist=join(root,'node_modules/@earendil-works/pi-tui/dist');
const {Markdown}=await import(pathToFileURL(join(dist,'components/markdown.js')).href);
const {setCapabilities}=await import(pathToFileURL(join(dist,'terminal-image.js')).href);
const {Chalk}=await import(pathToFileURL(join(root,'node_modules/chalk/source/index.js')).href);
const chalk=new Chalk({level:3});
function theme(){return {heading:s=>chalk.bold.cyan(s),link:s=>chalk.blue(s),linkUrl:s=>chalk.dim(s),code:s=>chalk.yellow(s),codeBlock:s=>chalk.green(s),codeBlockBorder:s=>chalk.dim(s),quote:s=>chalk.italic(s),quoteBorder:s=>chalk.dim(s),hr:s=>chalk.dim(s),listBullet:s=>chalk.cyan(s),bold:s=>chalk.bold(s),italic:s=>chalk.italic(s),strikethrough:s=>chalk.strikethrough(s),underline:s=>chalk.underline(s)}}
const emit=(name,lines)=>console.log(JSON.stringify({name,lines}));
for(const c of JSON.parse(readFileSync('test/parity/testdata/markdown-component-corpus.json','utf8'))){
 setCapabilities({images:null,trueColor:true,hyperlinks:c.links??false});
 const th=theme();if(c.quoteRGB){th.quote=s=>`\x1b[38;2;18;52;86m${s}\x1b[39m`;th.link=s=>`\x1b[38;2;129;162;190m${s}\x1b[39m`}
 const style=c.color||c.italic?{color:c.color?s=>chalk[c.color](s):undefined,italic:c.italic??false}:undefined;
 const md=new Markdown(c.source,c.paddingX??0,c.paddingY??0,th,style,{preserveOrderedListMarkers:c.preserveMarkers??false,preserveBackslashEscapes:c.preserveEscapes??false,renderLatex:c.renderLatex});
 emit(c.name,md.render(c.width));
}
const calls=[],frames=[];
const md=new Markdown('source',2,0,theme(),undefined,{transform:(source,availableWidth)=>{calls.push({source,availableWidth});return `${source} ${availableWidth}`}});
frames.push(md.render(80));md.render(80);frames.push(md.render(60));md.setText('updated');frames.push(md.render(60));md.invalidate();frames.push(md.render(60));console.log(JSON.stringify({name:'transform-cache',calls,frames}));
const backgrounds=[];const bg=new Markdown('alpha\nbeta',1,2,theme(),{bgColor:s=>{backgrounds.push(s);return `\x1b[44m${s}\x1b[49m`}});const bgLines=bg.render(12);console.log(JSON.stringify({name:'background-order',calls:backgrounds,lines:bgLines}));
setCapabilities({images:null,trueColor:true,hyperlinks:false});
const themeModule=await import(pathToFileURL(join(root,'dist/modes/interactive/theme/theme.js')).href);themeModule.initTheme('dark',false);
const {UserMessageComponent}=await import(pathToFileURL(join(root,'dist/modes/interactive/components/user-message.js')).href);
const {AssistantMessageComponent}=await import(pathToFileURL(join(root,'dist/modes/interactive/components/assistant-message.js')).href);
emit('stock-nested-style',new Markdown('**nested**',0,0,themeModule.getMarkdownTheme(),{bold:true}).render(24));
emit('user-preserve-options',new UserMessageComponent('1. first\n1. second\n\n"\\"').render(24));
emit('user-zones',new UserMessageComponent('hello').render(20));
const order=[],widths=[];
const user=new UserMessageComponent('The input is $x^2$.',undefined,1,[(source,context)=>{order.push('formula');widths.push(context.availableWidth);assert.deepEqual(context,{messageType:'user',isStreaming:false,availableWidth:78});return source.replace('$x^2$','x²')},source=>{order.push('suffix');return source+' Done.'}]);
const userLines=user.render(80);console.log(JSON.stringify({name:'user-transform',calls:order,widths,lines:userLines}));
let suffix='before';const changed=new UserMessageComponent('Message',undefined,1,[source=>source+' '+suffix]);const before=changed.render(80);suffix='after';changed.invalidate();const after=changed.render(80);console.log(JSON.stringify({name:'user-invalidation',frames:[before,after]}));
suffix='before';const padded=new UserMessageComponent('Message',undefined,1,[source=>source+' '+suffix]);const paddingFrames=[];for(const pad of [1,0,1]){padded.setOutputPad(pad);paddingFrames.push(padded.render(24));suffix='after';}console.log(JSON.stringify({name:'user-padding-transform',frames:paddingFrames}));
const message={role:'assistant',content:[{type:'text',text:'日本語テスト hello world 你好世界 test'},{type:'toolCall',id:'call',name:'tool',arguments:{}}],api:'openai-completions',provider:'test',model:'test',usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'toolUse',timestamp:0};
emit('assistant-padding',new AssistantMessageComponent(message).render(32));
const {createMermaidMarkdownTransformer}=await import(pathToFileURL(join(root,'dist/modes/interactive/components/mermaid.js')).href);
const builtIn=createMermaidMarkdownTransformer({getMode:()=> 'streaming',theme:themeModule.theme});
emit('user-builtin-transform',new UserMessageComponent('```mermaid\nflowchart LR\nA --> B\n```',undefined,1,[builtIn]).render(80));
