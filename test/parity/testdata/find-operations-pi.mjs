import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import assert from "node:assert/strict";
import {readFileSync,mkdtempSync,writeFileSync,rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join,resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=process.env.PI_PACKAGE_ROOT??resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version,PI_PACKAGE_VERSION);
const {createFindToolDefinition}=await import(pathToFileURL(join(root,"dist/core/tools/find.js")));
const text=result=>result.content.filter(block=>block.type==="text").map(block=>block.text).join("\n");
const print=(name,result)=>console.log("FIND_OPS "+name+" "+JSON.stringify([text(result),result.details??null]));
const tool=createFindToolDefinition("/",{operations:{exists:()=>true,glob:()=>["/home/user/project/","/home/user/project/file.txt"]}});
const result=await tool.execute("call-1",{pattern:"**"});
assert.equal(text(result),"home/user/project/\nhome/user/project/file.txt");print("root",result);
// Pi main 676af1d77 (#11747): a limit below 1 reads as 1, a fraction is floored, and a glob that ignores the limit is cut to it.
for(const limit of [0,1.5]){
 const tool=createFindToolDefinition("/",{operations:{exists:()=>true,glob:()=>["a","b"]}});
 const result=await tool.execute("call",{pattern:"*",limit});
 assert.deepEqual(result.details,{resultLimitReached:1});
 assert.equal(text(result),"a\n\n[1 results limit reached]");
 print("limit-"+limit,result);
}
const dir=mkdtempSync(join(tmpdir(),"find-zero-"));
try{
 writeFileSync(join(dir,"a.txt"),"");
 const result=await createFindToolDefinition(dir).execute("call",{pattern:"*.txt",limit:0});
 assert.equal(result.details,undefined);
 assert.equal(text(result),"a.txt");
 print("fd-zero",result);
}finally{rmSync(dir,{recursive:true,force:true});}
