// PiG's legacy sidecars are an upgrade input, not part of Pi's file format.
// Compare the upgraded CLI with a normal Pi store; check every output byte.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, utimesSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const side = process.argv[2];
const root = mkdtempSync(join(tmpdir(), 'lock-upgrade-'));
const agent = join(root, 'agent'), cwd = join(root, 'work');
const command = side === 'pig' ? process.env.PIG_PARITY_PIG_BIN : process.env.PIG_PARITY_PI_BIN;
assert(command, 'missing real CLI binary');
try {
  mkdirSync(agent);
  mkdirSync(join(cwd, '.agents', 'skills'), {recursive:true});
  writeFileSync(join(agent, 'settings.json'), JSON.stringify({enableInstallTelemetry:false}));
  writeFileSync(join(agent, 'trust.json'), JSON.stringify({[cwd]:false}));
  writeFileSync(join(agent, 'auth.json'), '{}');
  const names = ['settings.json', 'trust.json', 'auth.json'];
  // Exact idle v0.2.0 sidecars: stale empty regular files, mode 0600. Held locks and model-cache writes are exercised through the actual stores in the Go tests.
  if (side === 'pig') for (const name of [...names, 'models-store.json']) {
    const path = join(agent, name+'.lock');
    writeFileSync(path, '', {mode:0o600});
    const old = new Date(Date.now() - 60000);
    utimesSync(path, old, old);
  }
  const env = {...process.env, HOME:root, USERPROFILE:root, PIG_HOME:join(root,'pig'), PIG_CODING_AGENT_DIR:agent, PI_CODING_AGENT_DIR:agent, PIG_USE_PI_DIRS:'', PIG_TEST_FAUX:'1', PIG_OFFLINE:'1', PI_OFFLINE:'1'};
  const args = ['--model', 'test-faux/faux-1', '--no-session', '--print', 'reply with exactly: UPGRADED'];
  if (side === 'pi') args.unshift('-e', resolve('test/parity/testdata/test-faux-provider.ts'));
  // The second invocation must see an ordinary Pi-compatible store, with no persistent file sidecars recreated by the new host.
  for (let turn = 0; turn < 2; turn++) {
    const result = spawnSync(command, args, {cwd, env, encoding:'utf8', timeout:30000});
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, 'UPGRADED\n');
    assert.equal(result.stderr, '');
    for (const name of names) assert(!existsSync(join(agent,name+'.lock')), name+' sidecar survived');
    process.stdout.write(result.stdout);
  }
} finally {
  rmSync(root, {recursive:true, force:true});
}
