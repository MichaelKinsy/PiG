import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,readFileSync,writeFileSync,chmodSync,rmSync,symlinkSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve,dirname} from 'node:path';
import {pathToFileURL} from 'node:url';
const root=resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');assert.equal(JSON.parse(readFileSync(join(root,'package.json'),'utf8')).version,'1.0.2');
const mod=await import(pathToFileURL(join(root,'dist/config.js')));
const temp=mkdtempSync(join(tmpdir(),'pi-config-')),bin=join(temp,'bin');mkdirSync(bin);
for(const command of ['npm','pnpm','yarn','bun']){const file=join(bin,command);writeFileSync(file,'#!/bin/sh\nif [ "$1" = "--prefix" ]; then shift 2; fi\ncase "$1" in root) printf "%s\\n" "$CONFIG_TEST_ROOT";; global) printf "%s\\n" "$CONFIG_TEST_GLOBAL";; pm) printf "%s\\n" "$CONFIG_TEST_BUN_BIN";; *) exit 1;; esac\n');chmodSync(file,0o755);}
process.env.PATH=bin+':'+process.env.PATH;
const current='@earendil-works/pi-coding-agent',old='@mariozechner/pi-coding-agent';
function install(owner,pkg,spaced=false){const prefix=mkdtempSync(join(temp,spaced?'pi prefix ':'prefix-'));let global=join(prefix,'lib'),modules=join(global,'node_modules'),bunBin='';if(owner==='pnpm'){modules=join(prefix,'pnpm/global/5/node_modules');global=dirname(modules);}if(owner==='yarn'){global=join(prefix,'yarn/global');modules=join(global,'node_modules');}if(owner==='bun'){bunBin=join(prefix,'.bun/bin');modules=join(prefix,'.bun/install/global/node_modules');}const packageDir=join(modules,pkg);mkdirSync(packageDir,{recursive:true});process.env.PI_PACKAGE_DIR=packageDir;process.env.CONFIG_TEST_ROOT=modules;process.env.CONFIG_TEST_GLOBAL=global;process.env.CONFIG_TEST_BUN_BIN=bunBin;let executable=join(packageDir,'dist/cli.js');if(owner==='pnpm')executable=join(modules,'.pnpm',pkg.replace('/','+')+'@0.0.0','node_modules',pkg,'dist/cli.js');if(owner==='yarn')executable=join(global,'.yarn',pkg,'dist/cli.js');Object.defineProperty(process,'execPath',{value:executable,configurable:true});assert.equal(mod.detectInstallMethod(),owner);return {prefix,packageDir};}
function emit(name,command,prefix='',spaced=false){assert(command);const token=spaced?'PREFIX SPACE':'PREFIX';function project(c){const v={command:c.command,args:c.args.map(arg=>prefix&&arg===prefix?token:arg),display:prefix?c.display.replaceAll(prefix,token):c.display};if(c.steps)v.steps=c.steps.map(project);return v;}console.log('CONFIG_COMMAND '+JSON.stringify({case:name,command:project(command)}));}
try {
for(const row of [
 {name:'self-updates npm installs from custom prefixes',installed:current},
 {name:'self-updates exact npm versions without uninstalling the current package',installed:current,target:{packageName:current,installSpec:current+'@1.2.3'}},
 {name:'self-updates renamed packages from the current install prefix',installed:old,target:'@new-scope/pi'},
 {name:'self-update respects configured npmCommand',installed:current,configured:true},
 {name:'self-update treats empty npmCommand as unset',installed:current,empty:true},
 {name:'quotes npm self-update display paths',installed:current,spaced:true},
]){const {prefix}=install('npm',row.installed,row.spaced);const argv=row.configured?['npm','--prefix',prefix]:row.empty?[]:undefined;emit(row.name,mod.getSelfUpdateCommand(row.installed,argv,row.target??row.installed),prefix,row.spaced);}
for(const row of [
 {name:'self-updates bun global installs from bun pm bin',owner:'bun',installed:current,target:current},
 {name:'self-updates renamed pnpm global installs by removing the old package first',owner:'pnpm',installed:old,target:'@new-scope/pi'},
 {name:'self-updates renamed yarn global installs by removing the old package first',owner:'yarn',installed:old,target:'@new-scope/pi'},
 {name:'self-updates renamed bun global installs by removing the old package first',owner:'bun',installed:old,target:'@new-scope/pi'},
]){install(row.owner,row.installed);emit(row.name,mod.getSelfUpdateCommand(row.installed,undefined,row.target));}
const pnpm=join(temp,'Library/pnpm'),global=join(pnpm,'global/v11'),logical=join(global,'11e9a/node_modules',current),store=join(pnpm,'store/v11/links',current,'0.75.0/hash/node_modules',current);mkdirSync(store,{recursive:true});writeFileSync(join(store,'package.json'),'{}');mkdirSync(dirname(logical),{recursive:true});symlinkSync(store,logical,'dir');process.env.PI_PACKAGE_DIR=store;process.env.CONFIG_TEST_ROOT=global;process.argv[1]=join(logical,'dist/cli.js');Object.defineProperty(process,'execPath',{value:join(store,'dist/cli.js'),configurable:true});assert.equal(mod.detectInstallMethod(),'pnpm');const command=mod.getSelfUpdateCommand(current);assert.deepEqual(command,{command:'pnpm',args:['install','-g','--ignore-scripts','--config.minimumReleaseAge=0',current],display:'pnpm install -g --ignore-scripts --config.minimumReleaseAge=0 '+current});console.log('CONFIG_PNPM_STORE resolved');
const {packageDir}=install('npm',current);chmodSync(packageDir,0o500);try{assert.equal(mod.getSelfUpdateCommand(current),undefined);assert.match(mod.getSelfUpdateUnavailableInstruction(current),/the install path is not writable/);console.log('CONFIG_READONLY command=absent hint=not-writable');}finally{chmodSync(packageDir,0o700);}
}finally{rmSync(temp,{recursive:true,force:true});}
