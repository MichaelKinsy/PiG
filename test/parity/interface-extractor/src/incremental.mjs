import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { PACKAGE_LIST, resolvePackages } from "./inventory.mjs";

/** Workspace packages each tracked package can reach through its manifest, transitively. Their sources are what its TypeScript program can read. */
export function workspaceClosures(sourceRoot) {
  const names = new Map(PACKAGE_LIST.map(({ key, name }) => [name, key]));
  const direct = new Map();
  for (const { key } of PACKAGE_LIST) {
    const manifestFile = path.join(sourceRoot, "packages", key, "package.json");
    const deps = new Set();
    if (fs.existsSync(manifestFile)) {
      const manifest = JSON.parse(fs.readFileSync(manifestFile, "utf8"));
      for (const field of ["dependencies", "devDependencies", "peerDependencies", "optionalDependencies"]) {
        for (const name of Object.keys(manifest[field] ?? {})) if (names.has(name)) deps.add(names.get(name));
      }
    }
    direct.set(key, deps);
  }
  const closures = new Map();
  for (const { key } of PACKAGE_LIST) {
    const seen = new Set([key]);
    const todo = [key];
    while (todo.length) {
      for (const dep of direct.get(todo.pop()) ?? []) {
        if (!seen.has(dep)) {
          seen.add(dep);
          todo.push(dep);
        }
      }
    }
    closures.set(key, [...seen].sort());
  }
  return closures;
}

// What the extraction's TypeScript program can read (inventory.mjs sets no resolveJsonModule, so JSON data is never loaded): code, declarations and manifests.
const PROGRAM_INPUT = /(\.(ts|tsx|mts|cts|js|mjs|cjs)|(^|\/)package\.json|(^|\/)tsconfig[^/]*\.json)$/;

/** Content hash of a directory: sorted relative paths and bytes, following symlinks, skipping nested node_modules unless asked. `all` hashes every file, not only the program's inputs. */
export function hashTree(directory, { nodeModules = false, all = false } = {}) {
  const hash = crypto.createHash("sha256");
  const walk = (current, prefix) => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))) {
      if (entry.name === "node_modules" && !nodeModules) continue;
      const file = path.join(current, entry.name);
      const stat = fs.statSync(file);
      if (stat.isDirectory()) walk(file, `${prefix}${entry.name}/`);
      else if (stat.isFile() && (all || PROGRAM_INPUT.test(`${prefix}${entry.name}`))) hash.update(`${prefix}${entry.name}\0${crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex")}\n`);
    }
  };
  if (fs.existsSync(directory)) walk(directory, "");
  return hash.digest("hex");
}

/**
 * One cache key per package. It covers everything the package's extraction reads: the sources of the package and of every workspace
 * package it can reach, the published trees of the same set, the extractor and the invocation. Third-party inputs are not in the key:
 * an entry also records the content hash of every third-party file its programs read, and is reused only while those files are unchanged.
 */
export function packageKeys({ sourceRoot, publishedRoot, fingerprint, publishedRoots }) {
  const closures = workspaceClosures(sourceRoot);
  const published = publishedRoots ?? new Map(resolvePackages({ origin: "published", root: publishedRoot, sourceRoot }).map((pkg) => [pkg.key, pkg.root]));
  const sourceHash = new Map();
  const publishedHash = new Map();
  const memo = (map, key, compute) => {
    if (!map.has(key)) map.set(key, compute());
    return map.get(key);
  };
  const keys = new Map();
  for (const { key } of PACKAGE_LIST) {
    if (!published.has(key)) continue;
    const parts = closures.get(key).map((member) => [
      member,
      memo(sourceHash, member, () => hashTree(path.join(sourceRoot, "packages", member))),
      memo(publishedHash, member, () => (published.has(member) ? hashTree(published.get(member)) : "absent")),
    ]);
    keys.set(key, crypto.createHash("sha256").update(JSON.stringify({ fingerprint, key, parts })).digest("hex"));
  }
  return keys;
}

/** Merge per-package extractions into the inventory a whole-tree extraction writes: packages in key order, interfaces sorted by ID. */
export function mergeParts(header, parts) {
  const packages = parts.flatMap((part) => part.packages).sort((a, b) => a.key.localeCompare(b.key));
  const interfaces = parts.flatMap((part) => part.interfaces).sort((a, b) => a.id.localeCompare(b.id));
  return { ...header, packages, interfaces };
}

/** True for a path relative to the node_modules root that lies in a third-party package: not inside a workspace package, unless inside that package's own nested node_modules. */
function isThirdParty(relative) {
  const inside = relative.match(/^@earendil-works\/[^/]+\/(.*)$/);
  if (!inside) return !relative.startsWith("..") && !relative.startsWith("@earendil-works/");
  return inside[1].startsWith("node_modules/");
}

/** [path relative to the node_modules root, content hash] of every third-party file in a footprint (absolute paths). */
export function thirdPartyFiles(nodeModulesRoot, files) {
  return files
    .map((file) => path.relative(nodeModulesRoot, file).replaceAll(path.sep, "/"))
    .filter(isThirdParty)
    .map((relative) => [relative, crypto.createHash("sha256").update(fs.readFileSync(path.join(nodeModulesRoot, relative))).digest("hex")])
    .sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
}

/** True while every recorded third-party file still has the recorded content. */
export function thirdPartyUnchanged(nodeModulesRoot, recorded) {
  return recorded.every(([relative, hash]) => {
    const file = path.join(nodeModulesRoot, relative);
    return fs.existsSync(file) && crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex") === hash;
  });
}

function readPart(directory, name) {
  const file = path.join(directory, name);
  return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : undefined;
}

function publish(directory, parts) {
  fs.mkdirSync(path.dirname(directory), { recursive: true });
  const staging = `${directory}.tmp-${process.pid}-${crypto.randomBytes(4).toString("hex")}`;
  fs.mkdirSync(staging);
  for (const [name, value] of Object.entries(parts)) fs.writeFileSync(path.join(staging, `${name}.json`), `${JSON.stringify(value)}\n`);
  try {
    fs.renameSync(staging, directory);
  } catch {
    fs.rmSync(staging, { recursive: true, force: true });
  }
}

/**
 * Source and published inventories of every tracked package, extracting only the packages whose key has no valid cache entry.
 * `extract(key)` returns {source, published, footprint} for one package (footprint: the absolute paths its programs read); it runs
 * with at most `jobs` calls in flight.
 */
export async function extractIncremental({ sourceRoot, publishedRoot, publishedRoots, nodeModulesRoot, header, cacheDir, jobs, fingerprint, extract }) {
  const keys = packageKeys({ sourceRoot, publishedRoot, fingerprint, publishedRoots });
  const results = new Map();
  const missing = [];
  for (const [key, hash] of keys) {
    const entry = cacheDir ? path.join(cacheDir, hash) : undefined;
    const source = entry && readPart(entry, "source.json");
    const published = entry && readPart(entry, "published.json");
    const recorded = entry && readPart(entry, "third-party.json");
    if (source && published && recorded && thirdPartyUnchanged(nodeModulesRoot, recorded)) results.set(key, { source, published });
    else missing.push(key);
  }
  const queue = [...missing];
  const worker = async () => {
    for (let key = queue.shift(); key !== undefined; key = queue.shift()) {
      const { source, published, footprint } = await extract(key);
      results.set(key, { source, published });
      if (cacheDir) publish(path.join(cacheDir, keys.get(key)), { source, published, "third-party": thirdPartyFiles(nodeModulesRoot, footprint) });
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, Math.min(jobs, missing.length)) }, worker));
  const ordered = [...keys.keys()].map((key) => results.get(key));
  return {
    source: mergeParts({ ...header, origin: "source" }, ordered.map((part) => part.source)),
    published: mergeParts({ ...header, origin: "published" }, ordered.map((part) => part.published)),
    extracted: missing.sort(),
    reused: [...keys.keys()].filter((key) => !missing.includes(key)).sort(),
  };
}
