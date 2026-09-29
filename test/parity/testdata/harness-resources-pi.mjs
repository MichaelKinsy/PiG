import { resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";

const base = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core/dist/harness");
const { NodeExecutionEnv } = await import(pathToFileURL(resolve(base, "env/nodejs.js")));
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(resolve(base, "context.js")));
const { loadSourcedPromptTemplates, formatPromptTemplateInvocation } = await import(pathToFileURL(resolve(base, "prompt-templates.js")));
const { loadSourcedSkills } = await import(pathToFileURL(resolve(base, "skills.js")));
const root = process.argv[2];
const env = new NodeExecutionEnv({ cwd: root });
for (const [path, content] of Object.entries({
  "prompts/one.md": "---\ndescription: One\n---\nHello $1",
  "prompts/nested/ignored.md": "Ignored",
  "skills/example/SKILL.md": "---\nname: example\ndescription: Example\n---\nUse this skill.",
  "broken/SKILL.md": "---\nname: broken\n---\nMissing description.",
})) {
  const written = await env.writeFile(path, content, BACKGROUND_CONTEXT);
  if (!written.ok) throw written.error;
}
const prompts = await loadSourcedPromptTemplates(env, [{ path: "prompts", source: "project" }], undefined, BACKGROUND_CONTEXT);
const skills = await loadSourcedSkills(env, [{ path: "skills", source: "user" }, { path: "broken", source: "external" }], undefined, BACKGROUND_CONTEXT);
if (prompts.diagnostics.length) throw new Error(JSON.stringify(prompts.diagnostics));
const records = prompts.promptTemplates.map(({ promptTemplate: t, source }) => ["prompt", t.name, t.description, t.content, source, formatPromptTemplateInvocation(t, ["world"])]);
records.push(...skills.skills.map(({ skill: s, source }) => ["skill", s.name, s.description, s.content, relative(root, s.filePath).replaceAll("\\", "/"), s.disableModelInvocation, source]));
records.push(...skills.diagnostics.map(d => ["warning", d.type, d.code, d.message, relative(root, d.path).replaceAll("\\", "/"), d.source]));
console.log(JSON.stringify(records));
