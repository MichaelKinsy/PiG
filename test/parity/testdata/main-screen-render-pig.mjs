// Stream frame observations, then observe the private interactive input caller through its regression test.
import { spawnSync } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { join } from 'node:path';
const env = { ...process.env, PI_TUI_DEBUG_REDRAW: '', PIG_HOME: mkdtempSync(join(process.argv[2], 'home-')) };
const frames = spawnSync('go', ['run', './test/parity/testdata/main-screen-render-pig', process.argv[2]], { stdio: 'inherit', env });
if (frames.status !== 0) throw frames.error ?? new Error(`frame probe exited ${frames.status}`);
const caller = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestInteractiveKeyboardRequestsImmediateRender$', '-v', '-count=1'], { encoding: 'utf8', env });
if (caller.status !== 0) { process.stderr.write(caller.stdout ?? ''); process.stderr.write(caller.stderr ?? ''); throw caller.error ?? new Error(`keyboard caller exited ${caller.status}`); }
const observation = /keyboard-render-observation:([^\n]+)/.exec(caller.stdout);
if (!observation) throw new Error('keyboard caller emitted no observation');
process.stdout.write(observation[1]+'\n');
