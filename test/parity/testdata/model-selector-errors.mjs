import { spawnSync } from 'node:child_process';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
if (process.argv[2] === 'pig') {
  const result = spawnSync('go', ['test','./internal/codingagent','-run','^TestModelSelectorListsEveryFailedCatalogUpstream$','-count=1','-v'], {encoding:'utf8'});
  if (result.status !== 0) throw new Error(result.stdout + result.stderr);
  const rows = result.stdout.split('\n').filter(line => line.startsWith('CATALOG-ERROR:'));
  if (rows.length !== 1) throw new Error(`Expected one computed diagnostic, received ${rows.length}`);
  console.log(rows[0].slice('CATALOG-ERROR:'.length));
} else {
  const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
  const load = path => import(pathToFileURL(join(root,'dist',path)).href);
  const {initTheme} = await load('modes/interactive/theme/theme.js');
  const {ModelSelectorComponent} = await load('modes/interactive/components/model-selector.js');
  const {stripAnsi} = await load('utils/ansi.js');
  initTheme('dark');
  let complete;
  const refreshed = new Promise(resolve => {complete=resolve;});
  let renders=0;
  const runtime={getAvailableSnapshot:()=>[],refresh:async()=>({aborted:false,errors:new Map([['openai',new Error('unavailable')],['anthropic',new Error('unavailable')]])})};
  const selector=new ModelSelectorComponent({requestRender:()=>{if(++renders===2)complete();}},undefined,runtime,[],()=>{},()=>{});
  try {
    await refreshed;
    const row=stripAnsi(selector.render(120).join('\n')).split('\n').find(line=>line.includes('Could not refresh'));
    if (!row) throw new Error('No diagnostic rendered');
    console.log(row.trimEnd());
  } finally { selector.dispose(); }
}
