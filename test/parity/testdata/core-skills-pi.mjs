import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";
const { loadSkills } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/skills.js")));
const root = process.argv[2];
const paths = [];
for (const [name, fields] of [["invalid", "description: true"], ["renamed", "name: true\ndescription: valid"], ["enabled", 'description: valid\ndisable-model-invocation: "true"']]) {
  const dir = join(root, name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "SKILL.md"), `---\n${fields}\n---\nbody`);
  paths.push(dir);
}
const result = loadSkills({ cwd: root, agentDir: join(root, "agent"), skillPaths: paths, includeDefaults: false });
const records = result.skills.map(s => ["skill", s.name, s.description, s.disableModelInvocation, relative(root, s.filePath).replaceAll("\\", "/"), s.sourceInfo.source, s.sourceInfo.scope]);
records.push(...result.diagnostics.map(d => ["warning", d.type, d.message, relative(root, d.path).replaceAll("\\", "/")]));
console.log(JSON.stringify(records));
