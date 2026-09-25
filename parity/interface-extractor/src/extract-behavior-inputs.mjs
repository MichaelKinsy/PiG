#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { extractBehaviorInputInventory } from "./behavior-input-inventory.mjs";

const args = new Map();
for (let index = 2; index < process.argv.length; index += 2) {
  const key = process.argv[index];
  const value = process.argv[index + 1];
  if (!key?.startsWith("--") || value === undefined) {
    process.stderr.write(`behavior-input-extractor: invalid argument sequence near ${key ?? "<end>"}\n`);
    process.exit(1);
  }
  args.set(key.slice(2), value);
}

try {
  const sourceRoot = args.get("source-root");
  const upstreamVersion = args.get("upstream-version");
  const out = args.get("out") ?? "-";
  if (!sourceRoot || !upstreamVersion) throw new Error("--source-root and --upstream-version are required");
  const inventory = extractBehaviorInputInventory({ sourceRoot: path.resolve(sourceRoot), upstreamVersion });
  const encoded = `${JSON.stringify(inventory, null, 2)}\n`;
  if (out === "-") process.stdout.write(encoded);
  else {
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.writeFileSync(out, encoded);
  }
} catch (error) {
  process.stderr.write(`behavior-input-extractor: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
