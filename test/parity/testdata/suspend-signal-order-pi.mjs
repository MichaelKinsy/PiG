// Pi 0.87.1 interactive-mode.ts:4278-4312. Replace only the stop syscall so the parent can acknowledge each real signal delivery before sending the next.
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=resolve(process.env.PI_PACKAGE_ROOT??"extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(`${root}/package.json`, "utf8")).version, "0.99.2");
const {InteractiveMode}=await import(pathToFileURL(`${root}/dist/modes/interactive/interactive-mode.js`).href);
const originalOn=process.on.bind(process), originalRemove=process.removeListener.bind(process), originalKill=process.kill.bind(process);
const wrappers=new Map();
process.on=function(event,listener){
 if(event!=="SIGINT")return originalOn(event,listener);
 const wrapped=(...args)=>{listener(...args);console.log("ignored");};
 wrappers.set(listener,wrapped);
 return originalOn(event,wrapped);
};
process.removeListener=function(event,listener){return originalRemove(event,wrappers.get(listener)??listener);};
process.kill=function(pid,signal){if(pid===0&&signal==="SIGTSTP"){console.log("armed");return true;}return originalKill(pid,signal);};
process.stdin.on("data",()=>process.exit(0));
InteractiveMode.prototype.handleCtrlZ.call({ui:{stop(){console.log("stopped");},start(){console.log("resumed");},requestRender(force){console.log(`render:${force}`);}}});
