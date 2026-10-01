import assert from "node:assert/strict";
import {readFileSync,mkdtempSync,writeFileSync,rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join,resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=process.env.PI_PACKAGE_ROOT??resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version,"0.99.2");
const {createFindToolDefinition}=await import(pathToFileURL(join(root,"dist/core/tools/find.js")));
const text=result=>result.content.filter(block=>block.type==="text").map(block=>block.text).join("\n");
const print=(name,result)=>console.log("FIND_OPS "+name+" "+JSON.stringify([text(result),result.details??null]));
const tool=createFindToolDefinition("/",{operations:{exists:()=>true,glob:()=>["/home/user/project/","/home/user/project/file.txt"]}});
const result=await tool.execute("call-1",{pattern:"**"});
assert.equal(text(result),"home/user/project/\nhome/user/project/file.txt");print("root",result);
for(const limit of [0,1.5]){
 const tool=createFindToolDefinition("/",{operations:{exists:()=>true,glob:()=>["a","b"]}});
 const result=await tool.execute("call",{pattern:"*",limit});
 assert.deepEqual(result.details,{resultLimitReached:limit});
 assert.equal(text(result),`a\nb\n\n[${limit} results limit reached]`);
 print("limit-"+limit,result);
}
const dir=mkdtempSync(join(tmpdir(),"find-zero-"));
try{
 writeFileSync(join(dir,"a.txt"),"");
 const result=await createFindToolDefinition(dir).execute("call",{pattern:"*.txt",limit:0});
 assert.deepEqual(result.details,{resultLimitReached:0});
 assert.equal(text(result),"a.txt\n\n[0 results limit reached. Use limit=0 for more, or refine pattern]");
 print("fd-zero",result);
}finally{rmSync(dir,{recursive:true,force:true});}
