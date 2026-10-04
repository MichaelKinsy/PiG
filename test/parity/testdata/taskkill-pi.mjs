import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {EventEmitter} from "node:events";
import {createRequire,syncBuiltinESMExports} from "node:module";
import {join,resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=process.env.PI_PACKAGE_ROOT??resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version,"1.0.1");
const require=createRequire(import.meta.url),childProcess=require("node:child_process"),spawn=childProcess.spawn;
const child=new EventEmitter();let captured,calls=0;
childProcess.spawn=(command,args,options)=>{captured={command,args,options};calls++;return child;};syncBuiltinESMExports();
const {killProcessTree}=await import(pathToFileURL(join(root,"dist/utils/shell.js")));
const platform=Object.getOwnPropertyDescriptor(process,"platform"),systemRoot=process.env.SystemRoot;
try{
 process.env.SystemRoot="C:\\CustomWindows";
 Object.defineProperty(process,"platform",{configurable:true,value:"win32"});
 killProcessTree(1234);
 assert.equal(captured.command,join("C:\\CustomWindows","System32","taskkill.exe"));
 assert.deepEqual(captured.args,["/F","/T","/PID","1234"]);
 assert.deepEqual(captured.options,{stdio:"ignore",detached:true,windowsHide:true});
 assert.doesNotThrow(()=>child.emit("error",new Error("spawn taskkill ENOENT")));
 assert.equal(calls,1);
 console.log("TASKKILL "+JSON.stringify([captured.command,captured.args,calls]));
}finally{
 Object.defineProperty(process,"platform",platform);
 if(systemRoot===undefined)delete process.env.SystemRoot;else process.env.SystemRoot=systemRoot;
 childProcess.spawn=spawn;syncBuiltinESMExports();
}
