import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, statSync, unlinkSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version, '1.0.0');
const pi = await import(pathToFileURL(join(root,'dist/index.js')).href);
const cwd = mkdtempSync(join(tmpdir(), 'tool-wire-'));
const sorted = value => Array.isArray(value) ? value.map(sorted) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, sorted(value[key])])) : value;
try {
  for (const [name, text] of Object.entries({'a.txt':'hello\nsecond\n', 'b.txt':'hello\n', 'large.txt':'line\n'.repeat(2001)})) writeFileSync(join(cwd,name),text);
  for (const [name, tool, args] of [
    ['read','read',{path:'a.txt'}], ['read limited','read',{path:'a.txt',limit:1}], ['read truncated','read',{path:'large.txt'}],
    ['write','write',{path:'written.txt',content:'ok'}], ['edit','edit',{path:'a.txt',edits:[{oldText:'second',newText:'changed'}]}],
    ['ls','ls',{}], ['ls fractional','ls',{limit:1.5}], ['ls limited','ls',{limit:1}], ['find','find',{pattern:'a.txt'}], ['find limited','find',{pattern:'a.txt',limit:1}],
    ['grep','grep',{pattern:'hello',path:'a.txt'}], ['grep fractional','grep',{pattern:'.',path:'a.txt',limit:1.5}], ['grep limited','grep',{pattern:'hello',path:'a.txt',limit:1}],
    ['bash','bash',{command:'printf hello'}], ['bash truncated','bash',{command:"printf '%060000d' 0"}],
  ]) {
    const factory = 'create' + tool[0].toUpperCase() + tool.slice(1) + 'Tool';
    let update;
    const result = await pi[factory](cwd).execute('call', args, undefined, partial => {
      if (partial.content.some(block => block.type === 'text' && block.text !== '')) update = partial;
    });
    if (result.details?.fullOutputPath) {
      const path = result.details.fullOutputPath;
      statSync(path); unlinkSync(path);
      for (const block of result.content) if (block.type === 'text') block.text = block.text.replaceAll(path,'OUTPUT_FILE');
      result.details.fullOutputPath = 'OUTPUT_FILE';
    }
    if (result.structuredContent) {
      // bash.ts:392 measures the wall clock; the probe keeps only its type on both sides.
      assert.equal(typeof result.structuredContent.wall_time_seconds, 'number');
      result.structuredContent.wall_time_seconds = 'NUMBER';
    }
    console.log('TOOL_WIRE ' + JSON.stringify(sorted({case:name,result})));
    if (name === 'bash') { assert(update); console.log('TOOL_WIRE ' + JSON.stringify(sorted({case:'bash output update',result:update}))); }
  }
  const wide = join(cwd,'wide'); mkdirSync(wide);
  for (let i=0; i<300; i++) writeFileSync(join(wide,`${String(i).padStart(3,'0')}-${'a'.repeat(190)}.txt`),'');
  const result = await pi.createLsTool(cwd).execute('call',{path:'wide',limit:1000});
  console.log('TOOL_WIRE '+JSON.stringify(sorted({case:'ls byte truncated',result})));
} finally { rmSync(cwd,{recursive:true,force:true}); }
