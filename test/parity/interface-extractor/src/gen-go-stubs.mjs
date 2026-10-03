#!/usr/bin/env node

// gen-go-stubs: compiling Go stubs, upstream test skeletons, and test-mapping
// rows for one upstream Pi package. See go-stubs.mjs for the ownership rules.
//
//   node src/gen-go-stubs.mjs --source-root ../../../.upstream/current \
//     --package protocol --out ../../../internal/experimental/protocol \
//     [--go-package protocol] [--repo-root ../../..] [--check] \
//     [--dependency-root <node_modules>[,...]] \
//     [--import <npm package>=<Go import path>[,...]] \
//     [--scope public|module] [--subpath ./testing[,...]] \
//     [--test-mapping ../interfaces/test-mapping-v1.0.0.json] [--report -]

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { applyTestMapping, defaultDependencyRoots, generateGoStubs, writeOutputs } from "./go-stubs.mjs";

const BOOLEAN_FLAGS = new Set(["check"]);

function parseArgs(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index++) {
    const key = argv[index];
    if (!key.startsWith("--")) throw new Error(`unexpected argument ${key}`);
    const name = key.slice(2);
    if (BOOLEAN_FLAGS.has(name)) {
      args[name] = true;
      continue;
    }
    const value = argv[++index];
    if (value === undefined) throw new Error(`${key} needs a value`);
    args[name] = value;
  }
  return args;
}

try {
  const args = parseArgs(process.argv.slice(2));
  for (const required of ["source-root", "package", "out"]) {
    if (!args[required]) throw new Error(`--${required} is required`);
  }
  const outDir = path.resolve(args.out);
  const repoRoot = path.resolve(args["repo-root"] ?? process.cwd());
  const result = generateGoStubs({
    sourceRoot: path.resolve(args["source-root"]),
    packageKey: args.package,
    outDir,
    goPackage: args["go-package"],
    repoRoot,
    scope: args.scope ?? "public",
    subpaths: args.subpath ? args.subpath.split(",").filter(Boolean) : undefined,
    imports: Object.fromEntries((args.import ?? "").split(",").filter(Boolean).map((pair) => pair.split("="))),
    dependencyRoots: args["dependency-root"] ? args["dependency-root"].split(",").map((root) => path.resolve(root)) : defaultDependencyRoots(repoRoot),
  });
  const written = writeOutputs(outDir, result.outputs, { check: Boolean(args.check) });
  for (const conflict of written.conflicts) process.stderr.write(`gen-go-stubs: refused: ${conflict}\n`);
  if (written.conflicts.length) process.exit(2);
  for (const change of written.changes) process.stderr.write(`gen-go-stubs: ${args.check ? "would " : ""}${change.action} ${path.relative(process.cwd(), change.target)}\n`);
  if (args.check && written.changes.length) process.exitCode = 1;
  if (args["test-mapping"] && !args.check) {
    const changed = applyTestMapping(path.resolve(args["test-mapping"]), result.rows);
    process.stderr.write(`gen-go-stubs: ${changed} test-mapping row(s) updated\n`);
  }
  const report = {
    package: args.package,
    goPackage: result.packageName,
    generated: result.report.generated,
    present: result.report.skipped,
    testRows: result.rows,
  };
  if (args.report) {
    const encoded = `${JSON.stringify(report, null, 2)}\n`;
    if (args.report === "-") process.stdout.write(encoded);
    else fs.writeFileSync(args.report, encoded);
  }
} catch (error) {
  process.stderr.write(`gen-go-stubs: ${error instanceof Error ? error.stack : String(error)}\n`);
  process.exitCode = 1;
}
