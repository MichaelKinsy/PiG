import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const { ModelConfig } = await import(pathToFileURL(path.resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/model-config.js")));
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "model-config-diagnostics-"));
const cwd = process.cwd();
try {
  process.chdir(dir);
  for (const [name, content] of [
    ["blank", ""],
    ["malformed", '{\n  "providers": {\n'],
    ["schema", '{"providers":{"custom":{"models":[{}]}}}'],
    ["directory", null],
    ["ordinary", '{"providers":{}}'],
    ["missing", null],
  ]) {
    const file = `${name}-models.json`;
    if (name === "directory") fs.mkdirSync(file);
    else if (content !== null) fs.writeFileSync(file, content);
    const config = await ModelConfig.load(file);
    console.log(`CASE ${name}\n${config.getError() ?? ""}\nEND`);
    if (content !== null) assert.equal(fs.readFileSync(file, "utf8"), content);
  }
} finally {
  process.chdir(cwd);
  fs.rmSync(dir, { recursive: true, force: true });
}
