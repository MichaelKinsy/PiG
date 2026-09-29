// Runs Pi (cli.js) or PiG (native binary) in RPC mode against server.mjs and prints, per run, the first assistant message_start message as one JSON line.
// usage: node drive.mjs <api> <node-bin> <pi-cli.js|pig-binary> [runs]
// Object keys are sorted (RPC33 compares parsed records, not insertion order). Clocks and Google's generated `${name}_${Date.now()}_${counter}` IDs are canonicalized; every other field is kept.
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
const [api, nodeBin, bin, runsArg] = process.argv.slice(2);
const runs = Number(runsArg ?? 1);
// `test-faux` needs no server: Pi loads the paired fixture extension and PiG builds test-faux in under PIG_TEST_FAUX.
const fixture = api === 'test-faux';
const testFauxExtension = new URL('../../../../test/parity/testdata/test-faux-provider.ts', import.meta.url).pathname;
const srv = fixture ? null : spawn(nodeBin, [new URL('./server.mjs', import.meta.url).pathname, api], {stdio:['ignore','pipe','inherit']});
const port = fixture ? 0 : await new Promise(r => createInterface({input: srv.stdout}).once('line', l => r(l.trim())));
const baseUrl = api === 'google-generative-ai' ? `http://127.0.0.1:${port}/v1beta` : api === 'anthropic-messages' || api === 'pi-messages' || api === 'bedrock-converse-stream' ? `http://127.0.0.1:${port}` : `http://127.0.0.1:${port}/v1`;
const isPig = !bin.endsWith('.js');
const sortKeys = v => Array.isArray(v) ? v.map(sortKeys) : v && typeof v === 'object' ? Object.fromEntries(Object.keys(v).sort().map(k => [k, sortKeys(v[k])])) : v;
const canonical = message => sortKeys( JSON.parse(JSON.stringify(message, (key, value) => key === 'timestamp' && typeof value === 'number' ? 0 : typeof value === 'string' && key === 'id' ? value.replace(/^(\w+)_\d{13}_\d+$/, '$1_<ms>_<n>') : value)));
for (let run = 0; run < runs; run++) {
  const dir = mkdtempSync(join(tmpdir(), 'd82-'));
  const agent = join(dir, 'agent'); mkdirSync(agent);
  // Codex extracts the ChatGPT account id from the API key's JWT claims and streams SSE only when settings select it.
  const apiKey = api === 'openai-codex-responses' ? 'h.' + Buffer.from(JSON.stringify({'https://api.openai.com/auth': {chatgpt_account_id: 'acct'}})).toString('base64') + '.s' : 'k';
  if (api === 'openai-codex-responses') writeFileSync(join(agent, 'settings.json'), JSON.stringify({transport: 'sse'}));
  writeFileSync(join(agent, 'models.json'), JSON.stringify({providers:{p:{api, baseUrl, apiKey, models:[{id:'strict',name:'strict',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:1000}]}}}));
  writeFileSync(join(dir, 'parity-read-target.txt'), 'x\n');
  const child = spawn(isPig ? bin : nodeBin, [...(isPig ? [] : [bin]), '--mode', 'rpc', '--offline', '--no-extensions', ...(fixture && !isPig ? ['-e', testFauxExtension] : []), '--model', fixture ? 'test-faux/faux-1' : 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], {cwd: dir, env: {...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, PIG_CODING_AGENT_DIR: agent, PIG_HOME: join(dir, 'pighome'), ...(fixture ? {PIG_TEST_FAUX: '1'} : {}), ...(api === 'bedrock-converse-stream' ? {AWS_BEDROCK_SKIP_AUTH: '1', AWS_BEDROCK_FORCE_HTTP1: '1'} : {})}, stdio:['pipe','pipe','inherit']});
  let start = null;
  const settled = await new Promise(resolve => {
    const timer = setTimeout(() => resolve(false), 20000);
    createInterface({input: child.stdout}).on('line', line => {
      let event; try { event = JSON.parse(line); } catch { return; }
      if (!start && event.type === 'message_start' && event.message?.role === 'assistant') start = event.message;
      if (event.type === 'agent_settled') { clearTimeout(timer); resolve(true); }
    });
    child.stdin.write(JSON.stringify({id:'read',type:'prompt',message:fixture ? 'Run: read parity-read-target.txt' : 'READ'})+'\n');
  });
  child.kill();
  await new Promise(r => child.once('exit', r));
  rmSync(dir, {recursive:true, force:true});
  console.log(JSON.stringify(settled ? canonical(start) : {unsettled: true}));
}
srv?.kill();
