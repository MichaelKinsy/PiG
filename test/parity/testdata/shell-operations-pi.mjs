import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.1.0");
const { createBashTool, createLocalShellOperations } = await import(pathToFileURL(join(root,"dist/core/tools/bash.js")));
const tool = createBashTool(process.cwd(),{operations:createLocalShellOperations("bash",()=>({shell:"/nonexistent-shell-path-xyz123",args:["-c"]}))});
let failure;
try { await tool.execute("call",{command:"echo test"}); } catch (error) { failure = error.message; }
assert.equal(failure,"spawn /nonexistent-shell-path-xyz123 ENOENT");
console.log("BASH_SPAWN "+failure);
const delegated = createBashTool(process.cwd(),{shellPath:"/custom/bash",operations:{exec:async()=>({exitCode:0})}});
const result = await delegated.execute("call",{command:"echo test"});
console.log("BASH_DELEGATE "+result.content.filter(c=>c.type==="text").map(c=>c.text).join("\n"));
