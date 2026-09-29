// Port the literal assertions in Pi 0.87.1 markdown.test.ts.
// node test/parity/testdata/markdown-upstream-tests.mjs | gofmt > tui/markdown_upstream_test.go
// Terminal-cell, callback-lifecycle and heterogeneous table cases use explicit Go ports named below.
import { readFileSync } from 'node:fs';
import ts from '../../../extensions/sdk-ts/node_modules/typescript/lib/typescript.js';
const path='.upstream/v0.87.1/packages/tui/test/markdown.test.ts';
const src=ts.createSourceFile(path,readFileSync(path,'utf8'),ts.ScriptTarget.Latest,true);
const q=JSON.stringify;
let types=new Map();
const special=new Map([
 ['caches transformed Markdown by source and available width','testMDTransformCache'],
 ['should not leak wrapped link styles into table borders or plain cells','testMDTableLinkStyles'],
 ['should restore the enclosing style after a wrapped table link','testMDQuoteTableStyles'],
 ['should not leak styles into following lines when rendered in TUI','testMDStyledInputLine'],
 ['should not leak h1 underline into padding when inline code is the last token','testMDHeadingPadding'],
 ['stabilizes partial closing fence rendering','testMDStreamingFences'],
]);
function fail(n){throw new Error(`unsupported ${ts.SyntaxKind[n.kind]} at ${src.getLineAndCharacterOfPosition(n.getStart(src)).line+1}: ${n.getText(src)}`)}
function type(n){
 if(ts.isIdentifier(n))return types.get(n.text)??'string';
 if(ts.isParenthesizedExpression(n))return type(n.expression);
 if(ts.isArrayLiteralExpression(n))return 'array';
 if(ts.isNewExpression(n))return 'markdown';
 if(ts.isNumericLiteral(n))return 'int';
 if(ts.isPropertyAccessExpression(n)&&n.name.text==='length')return 'int';
 if(ts.isBinaryExpression(n)){if(n.operatorToken.kind===ts.SyntaxKind.BarBarToken&&type(n.left)==='array')return 'array';if(['+','-'].includes(n.operatorToken.getText(src)))return type(n.left);return 'bool'}
 if(ts.isCallExpression(n)&&ts.isPropertyAccessExpression(n.expression)){
  const name=n.expression.name.text;
  if(['render','map','filter','split','match'].includes(name))return 'array';
  if(name==='slice')return type(n.expression.expression);
  if(['indexOf','lastIndexOf','findIndex'].includes(name))return 'int';
  if(['some','includes','startsWith','endsWith','test'].includes(name))return 'bool';
 }
 return 'string';
}
function regex(n){const raw=n.getText(src),end=raw.lastIndexOf('/');return q(raw.slice(1,end));}
function arrow(n,result='string'){
 if(ts.isIdentifier(n)&&n.text==='stripAnsi')return 'mdStripSGR';
 if(!ts.isArrowFunction(n)||ts.isBlock(n.body))return fail(n);
 return 'func('+n.parameters.map(p=>p.name.getText(src)+' string').join(',')+') '+result+' { return '+expr(n.body)+' }';
}
function config(n,kind){
 if(!n||n.kind===ts.SyntaxKind.UndefinedKeyword||n.getText(src)==='undefined')return 'nil';
 if(!ts.isObjectLiteralExpression(n))return fail(n);
 return '&'+kind+'{'+n.properties.map(p=>{
  const key=p.name.getText(src),v=p.initializer;
  if(kind==='DefaultTextStyle'&&key==='color')return 'Color:mdChalk('+q(v.body.expression.name.text)+')';
  const fields={italic:'Italic',bold:'Bold',strikethrough:'Strikethrough',underline:'Underline',preserveOrderedListMarkers:'PreserveOrderedListMarkers',preserveBackslashEscapes:'PreserveBackslashEscapes',renderLatex:'RenderLatex'};
  if(!fields[key])return fail(p);
  return fields[key]+':'+(key==='renderLatex'?'new('+expr(v)+')':expr(v));
 }).join(',')+'}';
}
function expr(n){
 if(ts.isIdentifier(n))return n.text==='undefined'?'nil':n.text;
 if(ts.isStringLiteral(n)||ts.isNoSubstitutionTemplateLiteral(n))return q(n.text);
 if(ts.isNumericLiteral(n))return n.text;
 if(n.kind===ts.SyntaxKind.TrueKeyword)return 'true';if(n.kind===ts.SyntaxKind.FalseKeyword)return 'false';if(n.kind===ts.SyntaxKind.NullKeyword)return 'nil';
 if(ts.isParenthesizedExpression(n))return '('+expr(n.expression)+')';
 if(ts.isPrefixUnaryExpression(n)){const op=n.operator===ts.SyntaxKind.ExclamationToken?'!':n.operator===ts.SyntaxKind.MinusToken?'-':null;if(!op)return fail(n);return op+expr(n.operand)}
 if(ts.isArrayLiteralExpression(n))return '[]string{'+n.elements.map(expr).join(',')+'}';
 if(ts.isTaggedTemplateExpression(n)){if(n.tag.getText(src)!=='String.raw')return fail(n);if(ts.isNoSubstitutionTemplateLiteral(n.template))return q(n.template.rawText??n.template.text);return template(n.template,true)}
 if(ts.isTemplateExpression(n))return template(n,false);
 if(ts.isNewExpression(n)){if(n.expression.getText(src)!=='Markdown')return fail(n);const a=n.arguments;return 'NewMarkdownWithOptions('+expr(a[0])+','+expr(a[1])+','+expr(a[2])+',mdUpstreamTheme(),'+config(a[4],'DefaultTextStyle')+','+config(a[5],'MarkdownOptions')+')'}
 if(ts.isPropertyAccessExpression(n)){if(n.name.text==='length')return 'mdLen('+expr(n.expression)+')';return fail(n)}
 if(ts.isElementAccessExpression(n))return expr(n.expression)+'['+expr(n.argumentExpression)+']';
 if(ts.isBinaryExpression(n)){
  const op=n.operatorToken.getText(src);
  if(op==='||'&&type(n.left)==='array')return expr(n.left);
  const ops={'===':'==','!==':'!=','&&':'&&','||':'||','+':'+','-':'-','<':'<','>':'>','<=':'<=','>=':'>='};
  if(!ops[op])return fail(n);return '('+expr(n.left)+' '+ops[op]+' '+expr(n.right)+')';
 }
 if(ts.isCallExpression(n)){
  if(ts.isIdentifier(n.expression)){
   if(n.expression.text==='stripAnsi')return 'mdStripSGR('+expr(n.arguments[0])+')';
   if(n.expression.text==='resetCapabilitiesCache')return 'ResetCapabilitiesCache()';
   if(n.expression.text==='setCapabilities'){
    const fields=n.arguments[0].properties.map(p=>{const k=p.name.getText(src);if(k==='images'&&p.initializer.kind===ts.SyntaxKind.NullKeyword)return '';if(k==='trueColor')return 'TrueColor:'+expr(p.initializer);if(k==='hyperlinks')return 'Hyperlinks:'+expr(p.initializer??p.name);return fail(p)}).filter(Boolean);
    return 'SetCapabilities(TerminalCapabilities{'+fields.join(',')+'})';
   }
   return fail(n);
  }
  if(!ts.isPropertyAccessExpression(n.expression))return fail(n);
  const obj=n.expression.expression,method=n.expression.name.text,args=n.arguments;
  if(obj.getText(src)==='Math'&&method==='max')return 'max('+args.map(expr).join(',')+')';
  if(method==='render')return expr(obj)+'.Render('+args.map(expr).join(',')+')';
  if(method==='setText')return expr(obj)+'.SetText('+args.map(expr).join(',')+')';
  if(method==='invalidate')return expr(obj)+'.Invalidate()';
  if(method==='replace'){if(args[0].kind!==ts.SyntaxKind.RegularExpressionLiteral)return fail(n);return 'mdReplace('+expr(obj)+','+regex(args[0])+','+expr(args[1])+')'}
  if(method==='test'){if(obj.kind!==ts.SyntaxKind.RegularExpressionLiteral)return fail(n);return 'mdMatch('+regex(obj)+','+expr(args[0])+')'}
  if(method==='match')return 'mdMatches('+expr(obj)+','+regex(args[0])+')';
  if(method==='map')return 'mdMap('+expr(obj)+','+arrow(args[0])+')';
  if(method==='some')return 'slices.ContainsFunc('+expr(obj)+','+arrow(args[0],'bool')+')';
  if(method==='filter')return 'mdFilter('+expr(obj)+','+arrow(args[0],'bool')+')';
  if(method==='find')return 'mdFind('+expr(obj)+','+arrow(args[0],'bool')+')';
  if(method==='findIndex')return 'slices.IndexFunc('+expr(obj)+','+arrow(args[0],'bool')+')';
  if(method==='at')return 'mdAt('+expr(obj)+','+args.map(expr).join(',')+')';
  if(method==='slice')return (type(obj)==='array'?'mdSlice':'mdStringSlice')+'('+expr(obj)+','+args.map(expr).join(',')+')';
  if(method==='indexOf')return (type(obj)==='array'?'slices.Index':'strings.Index')+'('+expr(obj)+','+args.map(expr).join(',')+')';
  if(method==='lastIndexOf')return 'strings.LastIndex('+expr(obj)+','+args.map(expr).join(',')+')';
  const f={includes:'strings.Contains',startsWith:'strings.HasPrefix',endsWith:'strings.HasSuffix',join:'strings.Join',split:'strings.Split',trim:'widthx.JSTrim',trimEnd:'widthx.JSTrimEnd'};
  if(f[method])return f[method]+'('+[expr(obj),...args.map(expr)].join(',')+')';
  return fail(n);
 }
 return fail(n);
}
function template(n,raw){let out=q(raw?(n.head.rawText??n.head.text):n.head.text);for(const s of n.templateSpans)out+=' + '+expr(s.expression)+' + '+q(raw?(s.literal.rawText??s.literal.text):s.literal.text);return '('+out+')'}
function statement(n){
 if(ts.isVariableStatement(n))return n.declarationList.declarations.map(d=>{
  if(ts.isArrayBindingPattern(d.name)){if(d.name.elements.length!==1)return fail(d);const name=d.name.elements[0].name.getText(src);types.set(name,'string');return name+' := mdAt('+expr(d.initializer)+',0)'}
  const name=d.name.getText(src);types.set(name,type(d.initializer));return name+' := '+expr(d.initializer);
 }).join('\n');
 if(ts.isForOfStatement(n)){const name=n.initializer.declarations[0].name.getText(src);types.set(name,'string');return 'for _, '+name+' := range '+expr(n.expression)+' {\n'+block(n.statement)+'\n}'}
 if(ts.isExpressionStatement(n)){
  const e=n.expression;
  if(ts.isCallExpression(e)&&e.expression.getText(src).startsWith('assert.')){
   const method=e.expression.name.text,a=e.arguments;
   if(method==='ok')return 'mdAssert(t,'+expr(a[0])+','+q(a[0].getText(src))+')';
   if(method==='strictEqual'||method==='deepStrictEqual')return 'mdEqual(t,'+expr(a[0])+','+expr(a[1])+')';
   if(method==='notStrictEqual')return 'mdNotEqual(t,'+expr(a[0])+','+expr(a[1])+')';
   return fail(e);
  }
  return expr(e);
 }
 return fail(n);
}
function block(n){return n.statements.map(statement).join('\n')}
const cases=[];function visit(n){if(ts.isCallExpression(n)&&n.expression.getText(src)==='it')cases.push(n);ts.forEachChild(n,visit)}visit(src);
console.log('// Code generated by test/parity/testdata/markdown-upstream-tests.mjs; DO NOT EDIT.\npackage tui\n\nimport ("testing";"slices";"strings";"github.com/MichaelKinsy/PiG/tui/widthx")\n\nfunc TestUpstreamMarkdown(t *testing.T) {');
for(const n of cases){types=new Map();const name=n.arguments[0].text,line=src.getLineAndCharacterOfPosition(n.getStart(src)).line+1;const body=special.has(name)?special.get(name)+'(t)':block(n.arguments[1].body);console.log(`// ${path}:${line}\nt.Run(${q(name)},func(t *testing.T){mdCaseSetup(t)\n${body}\n})`)}
console.log('}');console.error(`${cases.length} upstream Markdown cases emitted`);
