// Compares PiG's first assistant message_start with the retained Pi oracle, per API, over N runs.
// usage: node check.mjs <pig-binary> <runs> [api...]   (exit 1 unless every run of every API equals pi-start.json)
// Regenerate the oracle: node check.mjs --oracle <pi cli.js> <runs> [api...]
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
const here = new URL('.', import.meta.url).pathname;
const args = process.argv.slice(2);
const oracleMode = args[0] === '--oracle';
if (oracleMode) args.shift();
const [bin, runs, ...only] = args;
const oraclePath = here + 'pi-start.json';
const oracle = JSON.parse(readFileSync(oraclePath, 'utf8'));
const apis = only.length ? only : Object.keys(oracle.apis);
let failed = false;
for (const api of apis) {
  const lines = execFileSync(process.execPath, [here + 'drive.mjs', api, process.execPath, bin, runs], {encoding: 'utf8', maxBuffer: 1 << 28}).trim().split('\n');
  const counts = new Map();
  for (const line of lines) counts.set(line, (counts.get(line) ?? 0) + 1);
  if (oracleMode) {
    if (counts.size !== 1) { console.error(`${api}: Pi produced ${counts.size} distinct states`); failed = true; continue; }
    oracle.apis[api] = JSON.parse(lines[0]);
    console.log(`${api}: Pi ${lines.length}/${lines.length} identical`);
    continue;
  }
  const want = JSON.stringify(oracle.apis[api]);
  const matched = counts.get(want) ?? 0;
  console.log(`${api}: ${matched}/${lines.length} equal Pi; ${counts.size} distinct state(s)`);
  if (matched !== lines.length) {
    failed = true;
    for (const [line, count] of counts) if (line !== want) console.log(`  ${count}x ${line}\n  want ${want}`);
  }
}
if (oracleMode) writeFileSync(oraclePath, JSON.stringify(oracle, null, 2) + '\n');
process.exit(failed ? 1 : 0);
