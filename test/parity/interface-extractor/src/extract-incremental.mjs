#!/usr/bin/env node
// Per-package, cached, parallel form of extract.mjs's paired mode. It writes the same two files a whole-tree extraction writes.
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { extractIncremental, hashTree } from "./incremental.mjs";
import { compareInventories, verifyPublishedSources } from "./inventory.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));

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

function run(command, env, commandArgs) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, commandArgs, { env: { ...process.env, ...env }, stdio: ["ignore", "ignore", "pipe"] });
    let stderr = "";
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.on("error", reject);
    child.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`${commandArgs.join(" ")} exited ${code}: ${stderr.trim()}`))));
  });
}

try {
  const args = parseArgs(process.argv.slice(2));
  const required = ["source-root", "published-root", "upstream-version", "source-out", "published-out"];
  for (const name of required) if (!args[name]) throw new Error(`--${name} is required`);
  const sourceRoot = path.resolve(args["source-root"]);
  const publishedRoot = path.resolve(args["published-root"]);
  const upstreamVersion = args["upstream-version"];
  const jobs = Number(args.jobs ?? Math.min(8, os.availableParallelism()));
  const fingerprint = crypto.createHash("sha256").update(`${hashTree(here)}\n${hashTree(path.resolve(here, "../../upstreampackages"), { all: true })}\n${ts.version}\n${process.version}\nincremental-format=1`).digest("hex");
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pig-extract-"));
  const result = await extractIncremental({
    sourceRoot,
    publishedRoot,
    header: { upstreamVersion, typescriptVersion: ts.version },
    cacheDir: args["cache-dir"],
    jobs,
    nodeModulesRoot: path.resolve(publishedRoot, "../.."),
    fingerprint,
    extract: async (key) => {
      const started = Date.now();
      const out = path.join(scratch, key);
      fs.mkdirSync(out, { recursive: true });
      await run(process.execPath, { PIG_INTERFACE_FOOTPRINT: path.join(out, "footprint.json") }, [
        "--max-old-space-size=3072", path.join(here, "extract.mjs"),
        "--source-root", sourceRoot, "--published-root", publishedRoot, "--upstream-version", upstreamVersion,
        "--package", key, "--source-out", path.join(out, "source.json"), "--published-out", path.join(out, "published.json"),
      ]);
      process.stderr.write(`interface-extractor: ${key} extracted in ${((Date.now() - started) / 1000).toFixed(1)}s\n`);
      return { source: JSON.parse(fs.readFileSync(path.join(out, "source.json"), "utf8")), published: JSON.parse(fs.readFileSync(path.join(out, "published.json"), "utf8")), footprint: JSON.parse(fs.readFileSync(path.join(out, "footprint.json"), "utf8")) };
    },
  });
  fs.rmSync(scratch, { recursive: true, force: true });
  const provenance = verifyPublishedSources({ publishedRoot, sourceRoot, upstreamVersion });
  const problems = [...compareInventories(result.source, result.published, { compareShapes: false }), ...provenance.problems];
  if (problems.length) throw new Error(`source/published inventory mismatch:\n${problems.map((problem) => `  - ${problem}`).join("\n")}`);
  for (const [file, value] of [[args["source-out"], result.source], [args["published-out"], result.published]]) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
  }
  process.stderr.write(`interface-extractor: extracted ${result.extracted.length} package(s) [${result.extracted.join(",")}], reused ${result.reused.length} [${result.reused.join(",")}]\n`);
} catch (error) {
  process.stderr.write(`interface-extractor: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
