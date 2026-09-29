// Execute the production layout path with the exact billion-length sparse fixture, without a public test-only API.
import {spawnSync} from 'node:child_process';
const run=spawnSync('go',['test','./tui','-run','^TestUpstreamViewportLayout$/paints_only_clipped_rows_from_very_large_scroll_content','-v','-count=1'],{encoding:'utf8'});
if(run.status!==0){process.stderr.write(run.stdout??'');process.stderr.write(run.stderr??'');throw run.error??new Error(`sparse layout probe exited ${run.status}`)}
const observation=/LAYOUT_SPARSE_OBSERVATION:([^\n]+)/.exec(run.stdout);
if(!observation)throw new Error('sparse layout probe emitted no observation');
process.stdout.write(observation[1]+'\n');
