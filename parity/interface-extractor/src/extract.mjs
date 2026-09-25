#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { compareInventories, extractInventory, verifyPublishedSources } from "./inventory.mjs";

function parseArgs(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key?.startsWith("--") || value === undefined) throw new Error(`invalid argument sequence near ${key ?? "<end>"}`);
    args[key.slice(2)] = value;
  }
  return args;
}

function writeJSON(file, value) {
  const encoded = `${JSON.stringify(value, null, 2)}\n`;
  if (file === "-") process.stdout.write(encoded);
  else {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, encoded);
  }
}

try {
  const args = parseArgs(process.argv.slice(2));
  const upstreamVersion = args["upstream-version"];
  if (!upstreamVersion) throw new Error("--upstream-version is required");

  const packageKeys = args.package ? args.package.split(",").filter(Boolean) : undefined;
  if (args["source-root"] && args["published-root"] && args["source-out"] && args["published-out"]) {
    const dependencyRoot = path.resolve(args["published-root"]);
    const source = extractInventory({ origin: "source", root: path.resolve(args["source-root"]), upstreamVersion, packageKeys, dependencyRoot });
    const published = extractInventory({ origin: "published", root: dependencyRoot, upstreamVersion, packageKeys, dependencyRoot });
    writeJSON(args["source-out"], source);
    writeJSON(args["published-out"], published);
    const provenance = verifyPublishedSources({
      publishedRoot: path.resolve(args["published-root"]),
      sourceRoot: path.resolve(args["source-root"]),
      upstreamVersion,
    });
    const problems = [
      ...compareInventories(source, published, { compareShapes: false }),
      ...provenance.problems,
    ];
    if (problems.length) throw new Error(`source/published inventory mismatch:\n${problems.map((problem) => `  - ${problem}`).join("\n")}`);
  } else {
    const origin = args.origin;
    const root = args.root;
    const out = args.out ?? "-";
    if (!root || !["source", "published"].includes(origin)) {
      throw new Error("use --origin source|published --root <path> [--out <path>], or the paired source/published arguments");
    }
    const dependencyRoot = args["dependency-root"] ? path.resolve(args["dependency-root"]) : (origin === "published" ? path.resolve(root) : undefined);
    writeJSON(out, extractInventory({ origin, root: path.resolve(root), upstreamVersion, packageKeys, dependencyRoot }));
  }
} catch (error) {
  process.stderr.write(`interface-extractor: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
