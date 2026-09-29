// Regenerate the literal Go port of the pinned overlay-non-capturing cases:
// node test/parity/testdata/overlay-non-capturing-tests.mjs | gofmt > tui/overlay_non_capturing_upstream_test.go
// This translator accepts only the constructs in this test file and rejects unknown statements.
import { readFileSync } from 'node:fs';
import ts from '../../../extensions/sdk-ts/node_modules/typescript/lib/typescript.js';
const path = '.upstream/v0.87.1/packages/tui/test/overlay-non-capturing.test.ts';
const text = readFileSync(path, 'utf8');
const source = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true);
const q = JSON.stringify;
const fail = n => { throw new Error(`unsupported ${ts.SyntaxKind[n.kind]}: ${n.getText(source)}`); };
const names = {setFocus:'SetFocus', addChild:'Add', clear:'Clear', showOverlay:'OpenOverlay', hide:'Close', focus:'focus', unfocus:'unfocus', setHidden:'setHidden', isFocused:'isFocused', hideOverlay:'hideOverlay', sendInput:'sendInput'};
function expr(n) {
 if (ts.isIdentifier(n)) return n.text;
 if (ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n)) return q(n.text);
 if (ts.isNumericLiteral(n)) return n.text;
 if (n.kind===ts.SyntaxKind.TrueKeyword) return 'true';
 if (n.kind===ts.SyntaxKind.FalseKeyword) return 'false';
 if (n.kind===ts.SyntaxKind.NullKeyword) return 'nil';
 if (ts.isParenthesizedExpression(n)) return '('+expr(n.expression)+')';
 if (ts.isArrayLiteralExpression(n)) return '[]string{'+n.elements.map(expr).join(',')+'}';
 if (ts.isPropertyAccessExpression(n)) {
  if(n.name.text==='focused') return expr(n.expression)+'.focused';
  return expr(n.expression)+'.'+(names[n.name.text]??n.name.text);
 }
 if(ts.isNewExpression(n)) {
  const a = (n.arguments??[]).map(expr);
  switch(n.expression.getText(source)) {
   case 'VirtualTerminal': return 'newNCUpstreamTerminal('+a.join(',')+')';
   case 'TuiMainScreen': return 'terminal.newTUI(t)';
   case 'FocusableOverlay': return '&ncUpstreamComponent{lines:'+a[0]+'}';
   case 'StaticOverlay': return '&recordingComponent{lines:'+a[0]+'}';
   case 'EmptyContent': return '&recordingComponent{}';
   case 'Container': return '&Container{}';
   default: return fail(n);
  }
 }
 if(ts.isCallExpression(n)) {
  if(ts.isPropertyAccessExpression(n.expression)) {
   const obj=expr(n.expression.expression), method=n.expression.name.text;
   if(method==='showOverlay' && n.arguments.length===1) return obj+'.OpenOverlay('+expr(n.arguments[0])+',OverlayOptions{})';
   if(method==='unfocus' && n.arguments.length) {
    const target=n.arguments[0].properties.find(p=>p.name.getText(source)==='target');
    return obj+'.unfocus('+expr(target.initializer??target.name)+')';
   }
   if(method==='getViewport') return obj+'.viewport()';
   if(method==='charAt') return 'ncFirstColumn('+expr(n.expression.expression)+')';
   return obj+'.'+(names[method]??method)+'('+n.arguments.map(expr).join(',')+')';
  }
  return expr(n.expression)+'('+n.arguments.map(expr).join(',')+')';
 }
 if(ts.isElementAccessExpression(n)) return expr(n.expression)+'['+expr(n.argumentExpression)+']';
 if(ts.isObjectLiteralExpression(n)) {
  return 'OverlayOptions{'+n.properties.map(p=> {
   const key=p.name.getText(source), value=p.initializer;
   if(['row','col','width'].includes(key)) return key+':overlayCells('+expr(value)+')';
   if(key==='visible') return 'visible:func(int,int)bool{return '+expr(value.body)+'}';
   if(key==='nonCapturing') return key+':'+expr(value);
   return fail(p);
  }).join(',')+'}';
 }
 if(ts.isBinaryExpression(n)) {
  const op=n.operatorToken.kind;
  if(op===ts.SyntaxKind.EqualsEqualsEqualsToken) return expr(n.left)+' == '+expr(n.right);
  if(op===ts.SyntaxKind.EqualsToken) return expr(n.left)+' = '+expr(n.right);
  return fail(n);
 }
 return fail(n);
}
function statement(n) {
 if(ts.isVariableStatement(n)) return n.declarationList.declarations.map(d=>expr(d.name)+' := '+expr(d.initializer)).join('\n');
 if(ts.isTryStatement(n)) return block(n.tryBlock);
 if(ts.isIfStatement(n)) return 'if '+expr(n.expression)+' {\n'+statement(n.thenStatement)+'\n}'+(n.elseStatement?' else {\n'+statement(n.elseStatement)+'\n}':'');
 if(ts.isBlock(n)) return block(n);
 if(ts.isExpressionStatement(n)) {
  let e=n.expression;
  if(ts.isAwaitExpression(e)) {
   if(e.expression.expression?.getText(source)==='renderAndFlush') return 'terminal.flush(t)';
   return fail(e);
  }
  if(ts.isBinaryExpression(e) && ts.isArrowFunction(e.right)) {
   const c=e.left.expression.getText(source);
   return c+'.onInput = func(data string) {\n'+block(e.right.body)+'\n}';
  }
  if(ts.isCallExpression(e)) {
   const full=e.expression.getText(source);
   if(full==='tui.start') return 'tui.Render()';
   if(full==='tui.stop') return '';
   if(full==='assert.strictEqual') return 'ncEqual(t,'+expr(e.arguments[0])+','+expr(e.arguments[1])+')';
   if(full==='assert.deepStrictEqual') return 'ncInputsEqual(t,'+expr(e.arguments[0])+','+expr(e.arguments[1])+')';
   if(full.endsWith('.inputs.push')) {
    const inputs=expr(e.expression.expression);
    return inputs+' = append('+inputs+','+e.arguments.map(expr).join(',')+')';
   }
   if(full==='tui.showOverlay' && e.arguments.length===1) return 'tui.OpenOverlay('+expr(e.arguments[0])+',OverlayOptions{})';
  }
  return expr(e);
 }
 return fail(n);
}
function block(n) {return n.statements.map(statement).join('\n');}
const cases=[];
function visit(n) {
 if(ts.isCallExpression(n) && n.expression.getText(source)==='it') cases.push(n);
 ts.forEachChild(n,visit);
}
visit(source);
console.log('// Code generated by test/parity/testdata/overlay-non-capturing-tests.mjs; DO NOT EDIT.\npackage tui\n\nimport "testing"\n\nfunc TestUpstreamOverlayNonCapturing(t *testing.T) {');
for(const n of cases) {
 const name=n.arguments[0].text;
 const line=source.getLineAndCharacterOfPosition(n.getStart(source)).line+1;
 let body;
 if(name.startsWith('microtask-deferred')) body='testNCUpstreamMicrotaskCleanup(t)';
 else body=block(n.arguments[1].body);
 console.log(`// ${path}:${line}\nt.Run(${q(name)},func(t *testing.T){\n${body}\n})`);
}
console.log('}');
console.error(`${cases.length} upstream cases emitted`);
