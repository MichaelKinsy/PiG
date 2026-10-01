import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "0.99.2");
const { SettingsManager } = await import(pathToFileURL(join(root, "dist/core/settings-manager.js")));
const { DefaultPackageManager } = await import(pathToFileURL(join(root, "dist/core/package-manager.js")));
const temp = mkdtempSync(join(tmpdir(), "pi-skill-metadata-"));
try {
  process.env.HOME = temp;
  const agent = join(temp, "agent");
  const file = join(agent, "skills", "my-skill", "SKILL.md");
  mkdirSync(join(file, ".."), { recursive: true });
  writeFileSync(file, "---\nname: test-skill\ndescription: A test skill\n---\nContent");
  const settingsManager = SettingsManager.inMemory({ skills: ["skills"] });
  const manager = new DefaultPackageManager({ cwd: temp, agentDir: agent, settingsManager });
  const result = await manager.resolve();
  const skill = result.skills.find((entry) => entry.path === file && entry.enabled);
  assert(skill);
  assert.equal(skill.metadata.source, "local");
  assert.equal(skill.metadata.scope, "user");
  assert.equal(skill.metadata.baseDir, undefined);
  console.log("SKILL_METADATA " + relative(agent, skill.path));
} finally {
  rmSync(temp, { recursive: true, force: true });
}
