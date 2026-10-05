import assert from "node:assert/strict";
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.3");
const cases = [
  ["read", "ctx-cwd-read.txt", "hello from ctx.cwd", {path:"ctx-cwd-read.txt"}],
  ["write", "", "", {path:"ctx-cwd-write.txt",content:"written via ctx.cwd"}, "written via ctx.cwd"],
  ["edit", "ctx-cwd-edit.txt", "old text", {path:"ctx-cwd-edit.txt",edits:[{oldText:"old text",newText:"new text"}]}, "new text"],
  ["grep", "ctx-cwd-grep.txt", "match in ctx.cwd", {pattern:"match"}],
  ["find", "ctx-cwd-find.txt", "find me", {pattern:"ctx-cwd-find.txt"}],
  ["ls", "ctx-cwd-ls.txt", "list me", {}],
  ["bash", "", "", {command:"pwd"}],
];
for (const [name,file,content,args,persisted] of cases) {
  const dir = mkdtempSync(join(tmpdir(),"tools-cwd-"));
  const fallback = mkdtempSync(join(tmpdir(),"tools-fallback-"));
  try {
    if(file) writeFileSync(join(dir,file),content);
    const module = await import(pathToFileURL(join(root,`dist/core/tools/${name}.js`)));
    const create = module[`create${name[0].toUpperCase()+name.slice(1)}ToolDefinition`];
    const result = await create(fallback, name === "bash" ? {exposeSessionEnvironment:false} : undefined).execute("call",args,undefined,undefined,{cwd:dir});
    if(persisted) assert.equal(readFileSync(join(dir,args.path),"utf8"),persisted);
    const text = result.content.filter(c=>c.type==="text").map(c=>c.text).join("\n");
    if(name==="bash") assert.equal(text.trim(),dir);
    console.log("TOOL_CWD "+JSON.stringify([name,text.replaceAll(dir,"__CWD__")]));
  } finally { rmSync(dir,{recursive:true,force:true}); rmSync(fallback,{recursive:true,force:true}); }
}
