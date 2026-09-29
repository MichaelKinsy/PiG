// Component observations followed by the real interactive user-message factory's built-in transform path.
import {spawnSync} from 'node:child_process';
const render=spawnSync('go',['run','./test/parity/testdata/markdown-components-pig'],{stdio:'inherit'});
if(render.status!==0)throw render.error??new Error(`component probe exited ${render.status}`);
const caller=spawnSync('go',['test','./internal/codingagent','-run','^TestInteractiveUserMessageRunsBuiltinMarkdownTransform$','-v','-count=1'],{encoding:'utf8'});
if(caller.status!==0){process.stderr.write(caller.stdout??'');process.stderr.write(caller.stderr??'');throw caller.error??new Error(`user factory probe exited ${caller.status}`)}
const record=/USER_BUILTIN_TRANSFORM:([^\n]+)/.exec(caller.stdout);
if(!record)throw new Error('user factory emitted no observation');
process.stdout.write(record[1]+'\n');
