// Generate with Node 26.7.0 / ICU 78.3 and the published Pi 0.87.1 package.
// Usage: node generate_sea_fixture.mjs ICU_SOURCE_DIR [PI_TUI_DIST_DIR]
import {readFileSync, writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {resolve, join} from 'node:path';
import {pathToFileURL, fileURLToPath} from 'node:url';

if (process.versions.node !== '26.7.0' || process.versions.icu !== '78.3') throw new Error('Expected Node 26.7.0 / ICU 78.3');
const root = fileURLToPath(new URL('./', import.meta.url));
const pi = resolve(process.argv[3] ?? join(root, '../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist'));
if (JSON.parse(readFileSync(join(pi, '../package.json'), 'utf8')).version !== '0.87.1') throw new Error('Expected Pi 0.87.1');
const {findWordBackward, findWordForward} = await import(pathToFileURL(join(pi, 'word-navigation.js')));
const segmenter = new Intl.Segmenter(undefined, {granularity:'word'});
const scripts = [
  ['thai', 'thaidict', '3166abde40c0f44ab91c28f5ce96d7d1472cb7882e1c0bda0a72f8f69dba4274', ['ภาษาไทย', 'ภาษาไทยภาษาไทย', 'สวัสดีครับยินดีต้อนรับ', 'ประเทศไทยมีประชากรมาก', 'กก', 'กกกก', 'กกกกก', 'ฯฯๆๆ', 'ภาษาไทยฯๆ', 'ภาษาไทยๆฯ', 'ภาษาไทยฯฯ', 'ภาษาไทยๆๆ'], '\u0e31', '\u0e01'],
  ['lao', 'laodict', '3c876934a3fa81031d2333525eafaca6a7c9f842e3b98f18c38880420afb5d36', ['ພາສາລາວ', 'ສະບາຍດີຊາວໂລກ', 'ປະເທດລາວ', 'ຂ້ອຍຮັກພາສາລາວ', 'ກກກກ', 'ກກກກກ', 'ໆໆ'], '\u0eb1', '\u0e81'],
  ['khmer', 'khmerdict', '87bee2d17cd5148aa36957eb05409eefc124de8ad519b81b789298ef3e60b5d9', ['ភាសាខ្មែរ', 'សួស្តីពិភពលោក', 'ប្រទេសកម្ពុជា', 'ខ្ញុំស្រឡាញ់ភាសាខ្មែរ', 'កកកក', 'កកកកក', 'ៗៗ'], '\u17d2', '\u1780'],
  ['burmese', 'burmesedict', '61d8abc3d9102b2f9bf0c9f44db0d7ab89b18172d8cd26832e4c83174bd8673b', ['မြန်မာဘာသာစကား', 'မင်္ဂလာပါကမ္ဘာ', 'မြန်မာနိုင်ငံ', 'ကျွန်တော်မြန်မာစကားပြောတတ်သည်', 'ကကကက', 'ကကကကက', '၏၌၍'], '\u1039', '\u1000'],
];
const corpus = new Map();
function add(script, text) { if (!corpus.has(text)) corpus.set(text, script); }
add('empty', '');
for (const [script, file, hash, phrases, mark, consonant] of scripts) {
  const source = readFileSync(join(process.argv[2], file+'.txt'));
  if (createHash('sha256').update(source).digest('hex') !== hash) throw new Error('Wrong dictionary: '+file);
  const words = source.toString('utf8').replace(/^\uFEFF/, '').split('\n').map(s=>s.trim()).filter(s=>s && !s.startsWith('#'));
  for (const text of phrases) {
    for (const variant of [text, text+text, ' '+text+' ', 'abc'+text+'xyz', '42'+text+'๑២', '你好'+text+'学生です', '😀'+text+'!', text+mark+consonant, mark+text, text+'\u200d'+text, text+'\u0301'+text, text+'\u200b'+text, text+'_'+text, text+'.'+text, text+"'"+text, '\t'+text+'\n', consonant+text.slice(1)]) add(script, variant);
  }
  // Evenly spaced dictionary samples and deterministic combinations exercise nested candidates and resynchronization, not just common phrases.
  for (let i=0; i<256; i++) {
    const word = words[Math.floor(i*words.length/256)];
    const next = words[(i*7919+97)%words.length];
    for (const text of [word, word+next, word.slice(0,-1)+mark+next, 'a'+word+','+next+'😀']) add(script,text);
  }
  // Truncated and unknown runs, including minimum span thresholds and combining-only input.
  for (let n=1; n<=12; n++) { add(script, consonant.repeat(n)); add(script, mark.repeat(n)); }
  const blockStart = {thai:0xe00, lao:0xe80, khmer:0x1780, burmese:0x1000}[script];
  const alphabet = Array.from({length:128}, (_,i)=>String.fromCodePoint(blockStart+i));
  for (const c of alphabet) {
    add(script, c+phrases[0]+c);
    add(script, mark+c+mark);
  }
  let seed = blockStart;
  const pick = () => { seed = (Math.imul(seed, 1664525)+1013904223)>>>0; return alphabet[(seed>>>16)%alphabet.length]; };
  for (let i=0; i<128; i++) add(script, Array.from({length:12}, pick).join(''));
}
for (const a of scripts) for (const b of scripts) add('mixed', a[3][0]+b[3][0]);
// ICU's dictionary trigger rules also exclude CJK/Hangul from ALetterPlus and retain rule-span endpoints across non-dictionary marks.
for (const text of ['abc한국xyz', 'ภาษาไทย한국abc', 'abc々xyz', 'カタカナ́カタカナ', 'カタカナ_abc', 'カタカナ_カタカナ', '学生́です', '한́글', '你好1中国', 'ภาษาไทยカタカナ_カタカナ']) add('rule-tailoring', text);
const ruleTokens = ['a', '1', '_', '한', '々', '字', 'カタカナ', '学生です', 'ภาษาไทย', 'ພາສາລາວ', 'မြန်မာဘာသာစကား', 'ភាសាខ្មែរ', 'א', "'", '😀'];
for (const a of ruleTokens) for (const b of ruleTokens) for (const middle of ['', '\u0301', '\u200d', '_', "'", '.']) add('rule-tailoring', a+middle+b);
for (const a of ruleTokens) for (const b of ruleTokens) add('rule-tailoring', a+b+'\u0301');
// populateDictionary starts engines only at $dictionary characters. UnhandledEngine claims only the latest script and precedes the factory on the per-call engine stack.
for (const text of ['＿ﾞヽ〆', '゛ﾞｶ', '1゛ｰ你好', '゛゛ ー你好', '你好 ゛゛ ー你好', '゛゛ ᨠᨠ ー你好', 'a゛_ᨠーゝ々', '゛\u3099ー你好', '〱〱ｰｰ学生', '゠゠ｶﾞﾞ', 'ｰﾞｶﾞ', 'ー你好', '゛゛ ー你好 你好 ー你好', '한국 ゛゛ ー你好', '゛ ー你好', '你好 ゛ｰ你好', '你好 〱〱ｰｰ学生']) add('engine-selection', text);
const cases = [...corpus].map(([text, script])=>({
  script, text,
  segments:[...segmenter.segment(text)].map(({segment,index,isWordLike})=>({segment,index,isWordLike})),
  backward:Array.from({length:text.length+1},(_,i)=>findWordBackward(text,i)),
  forward:Array.from({length:text.length+1},(_,i)=>findWordForward(text,i)),
}));
const header = {node:process.versions.node, icu:process.versions.icu, unicode:process.versions.unicode, pi:'0.87.1'};
writeFileSync(join(root,'testdata/sea-icu78.json'), JSON.stringify(header).slice(0,-1)+',"cases":[\n'+cases.map(c=>JSON.stringify(c)).join(',\n')+'\n]}\n');
console.log(`${cases.length} corpus texts, ${cases.reduce((n,c)=>n+c.forward.length+c.backward.length,0)} directional cursor probes`);
