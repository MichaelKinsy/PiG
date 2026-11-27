// Post-release Pi fixes that the vendored Pi JavaScript (shims/pi-dist) and the parity oracle carry ahead of the pin.
//
// PiG pins one Pi build, and the Node extension runtime runs that package's compiled output: the SDK (createAgentSession, the built-in
// tools, the llama.cpp built-in extension), pi-tui components extensions render with, and the HTML export. Go ports of later Pi fixes do
// not change those files, so a Node path would keep the old behavior. Each entry below (a BEGIN/END block named by the Pi commit it
// carries) applies that commit's change to the compiled output with exact before/after strings, and the vendoring step fails when a
// `before` string is missing or ambiguous (a pin change cannot silently drop a backport). This is Pi's own later behavior, not a divergence.
//
// An entry lives only until the pin reaches its commit: `make pin-bump` (automation/pin/carried.py) classifies every entry against the
// target Pi's installed package and deletes the blocks the target already carries (`--classify`); a block the target does not carry must
// still apply, else the bump stops naming the entry and the file. A script with no block is a no-op: the mechanism stays for the next
// release cycle, and the oracle build does not apply or stamp it (oracle.backports_digest is "" then).
//
// Run through vendor-pi-dist.sh after the identity patches and before the SDK and library bundles are compiled.
// The same entries are applied to the installed Pi the parity tests compare with (`--packages name=dir ...`, from automation/pin/oracle.py), so the oracle is Pi with these fixes.
// `pi` itself runs the package's bundle (dist/bundle, Pi's esbuild output over the compiled output), which no entry can patch as text: the oracle build applies
// the entries to the source build's packages too (`--package-files` names the files, so the build can put them back) and has Pi's own bundler make the bundle again.
// The source mirror (.upstream/current) stays the pinned build, which the sync ledger's delta audit compares with the earlier pin; the
// tests that read the mirror's docs and schemas read them through `--read-mirror <mirror> <path>` (internal/upstreammirror.ReadPicked), the
// pinned file plus the entries of packageFileBackports and backportPatches that cover it, and the TypeScript sources a mirrorSource entry covers.
// `--mirror-paths` lists the mirror files a backport covers (internal/upstreammirror.PickedPaths), so a test that reads many mirror files reads only those through `--read-mirror`.
// `--verify-packages` and `--verify-mirror` fail when an installed Pi does not carry each entry once, or a mirror file is not the pinned build's.
// A commit that also moves a dependency a Pi package pins exactly carries that as a `dependency(...)` entry in a block of its own (oracle only; see `dependency` below).
// What a commit's blocks deliberately leave at the pin is said in them, one `// not carried: <file> (<what>): <reason>` line per file. The sync ledger rows of a followed
// commit answer for the oracle too (automation/pin/carried.py oracle_ledger_problems, which `pin.py check-oracle` runs).

import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const note = (sha) => `// Pi ${sha} (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.\n`;

// 49fe0a237 (#11987): the supportsMessageThreads description of pi-ai's compat schema, which the models docs and the models.json schema repeat.
const messageThreadsDefault = ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"];

const blocks = [];
const patch = (id, key, patches) => blocks.push({ id, key, patches, table: "dist" });
// dependency carries a dependency change of a Pi commit: the Pi package `pkg` (pi-ai) pins `dep.name` exactly, at `dep.from` in the pinned build and at `dep.to` from
// the commit on. `resolved`, `integrity` and `dependencies` are the entry of `dep.to` in Pi's own package-lock.json at the commit (read from the Pi git, never from the
// registry). The oracle build (automation/pin/oracle.py carried_closure) installs the frozen closure with this lock entry in place of the pinned one, so `npm ci`
// verifies the archive against the integrity, and has Pi's bundler read the installed copy. No file of the installed package is rewritten (its package.json keeps the
// pin's range: published ranges follow the pin), and the vendored runtime installs no carried dependency. `--classify` reads the package's package.json (classifyDependency).
const dependency = (id, pkg, dep) => blocks.push({ id, key: `${pkg}/package.json`, table: "dependency", dependency: { id, package: pkg, ...dep } });
const packageFile = (id, key, patches) => blocks.push({ id, key, patches, table: "package" });
// A Pi TypeScript source that tests read from the source mirror and that a backport of its compiled output changes, by path in the Pi
// repository, with the same change as Pi's commit makes to the source. It stands in a block of its commit, so the bump that retires the
// commit's entries retires it with them.
const mirrorSource = (id, path, patches) => blocks.push({ id, key: path, patches, table: "mirror" });

// backportPatches keys the compiled output of a Pi package; packageFileBackports the files that are not compiled output and that the vendored runtime
// does not carry, but that the installed Pi package (the parity oracle) and the source mirror hold, with the same text. Both are keyed by package and path.
// Blocks of several Pi commits can name one file: their patches add up, in the order of the blocks in this script.
const tableOf = (name) => {
  // The table of the blocks whose table is `name`: key -> patches. The patches of every block of a key are joined in the order the blocks
  // are written (two Pi commits can change one file); classification stays per block. Blocks of other tables are not read, so a block of
  // another kind need not have patches. What cannot work fails here, when the script loads, naming both blocks: two patches that replace
  // the same text of a key (the second would find nothing to replace), and a key that one block names with patch and another with
  // packageFile (patchesOf reads one of those two tables).
  const table = {};
  const owners = new Map();
  const twin = name === "dist" ? "package" : name === "package" ? "dist" : undefined;
  for (const block of blocks.filter((b) => b.table === name)) {
    const { id, key, patches } = block;
    const other = blocks.find((b) => b.table === twin && b.key === key);
    if (other !== undefined) {
      const [first, second] = blocks.indexOf(other) < blocks.indexOf(block) ? [other.id, id] : [id, other.id];
      throw new Error(`backport blocks ${first} and ${second} disagree on ${key}: one names it with patch (compiled output), the other with packageFile`);
    }
    for (const [before] of patches) {
      const place = `${key}\n${before}`;
      if (owners.has(place)) {
        throw new Error(`backport blocks ${owners.get(place)} and ${id} both replace the same text of ${key}: ${JSON.stringify(before.slice(0, 80))}`);
      }
      owners.set(place, id);
    }
    (table[key] ??= []).push(...patches);
  }
  return table;
};
export const backportPatches = tableOf("dist");
export const packageFileBackports = tableOf("package");
// mirrorSourceBackports keys the mirrorSource entries by the path of the source in the Pi repository (packages/ai/src/providers/x.ts).
export const mirrorSourceBackports = tableOf("mirror");
// carriedDependencies lists the `dependency` entries: {id, package, name, from, to, resolved, integrity, dependencies}.
export const carriedDependencies = blocks.filter((b) => b.dependency).map((b) => b.dependency);

const patchesOf = (file) => backportPatches[file] ?? packageFileBackports[file] ?? [];

// patchBackports returns source, the text of file (a key of backportPatches or packageFileBackports), with its backports applied.
export function patchBackports(file, source) {
  for (const [before, after] of patchesOf(file)) {
    if (source.split(before).length !== 2) throw new Error(`Pi source changed in ${file}, a post-v1.5.2 backport no longer applies: ${before}`);
    source = source.replace(before, () => after);
  }
  return source;
}

// checkProviderDataManifest throws when a data file of `dir` (pi-ai's providers/data) is not the one its hidden manifest records, or the manifest names a file the
// directory lacks: Pi writes the data and .manifest.json in one generation, so a backport that changes a data file must change its manifest entry in the same
// run (a block for pi-ai/providers/data/.manifest.json), or check-model-data fails on the result. The structure hash is the Go validator's to check
// (cmd/check-model-data, TestPublishedModelDataValidates). A directory with no manifest is not checked.
export function checkProviderDataManifest(dir) {
  const manifestPath = join(dir, ".manifest.json");
  if (!existsSync(manifestPath)) return;
  const files = JSON.parse(readFileSync(manifestPath, "utf8")).files;
  const problems = [];
  for (const name of readdirSync(dir).filter((name) => name.endsWith(".json") && !name.startsWith(".")).sort()) {
    const recorded = files[name];
    if (recorded === undefined) problems.push(`${name} is not in the manifest`);
    else if (createHash("sha256").update(readFileSync(join(dir, name))).digest("hex") !== recorded) problems.push(`${name} does not match its manifest hash`);
  }
  for (const name of Object.keys(files)) if (!existsSync(join(dir, name))) problems.push(`${name} is in the manifest and not in the directory`);
  if (problems.length > 0) throw new Error(`${dir}: the provider data and its manifest differ after the backports (add a pi-ai/providers/data/.manifest.json block with the hashes of the changed files):\n${problems.join("\n")}`);
}

// applyBackportPatches rewrites the vendored files under dist (shims/pi-dist).
export function applyBackportPatches(dist) {
  for (const file of Object.keys(backportPatches)) {
    const path = join(dist, file);
    writeFileSync(path, patchBackports(file, readFileSync(path, "utf8")));
  }
  checkProviderDataManifest(join(dist, "pi-ai", "providers", "data"));
}

// packageLocation is where the file of package `name` with the path `rest` (the segments of its backport key after the package)
// lives in an installed Pi package: the compiled output under dist/, the docs, schemas and examples at the package root.
const packageLocation = (name, rest) => (name === "pi-coding-agent" && (rest[0] === "docs" || rest[0] === "schemas" || rest[0] === "examples") ? rest : ["dist", ...rest]).join("/");

const packageBackportKeys = () => [...Object.keys(backportPatches), ...Object.keys(packageFileBackports)];

// applyPackageBackports applies the backports of each package in `packages` (a short name such as "pi-tui" and the root directory
// of that Pi package, as `npm ci` installs it) in place. The oracle the parity tests run (automation/pin/oracle.py) is that installed
// Pi, so it carries what the Go port carries.
export function applyPackageBackports(packages) {
  for (const file of packageBackportKeys()) {
    const [name, ...rest] = file.split("/");
    if (!(name in packages)) continue;
    const path = join(packages[name], packageLocation(name, rest));
    writeFileSync(path, patchBackports(file, readFileSync(path, "utf8")));
  }
  if ("pi-ai" in packages) checkProviderDataManifest(join(packages["pi-ai"], "dist", "providers", "data"));
}

// packageBackportFiles lists the files applyPackageBackports rewrites under `packages`.
export function packageBackportFiles(packages) {
  return packageBackportKeys().flatMap((file) => {
    const [name, ...rest] = file.split("/");
    return name in packages ? [join(packages[name], packageLocation(name, rest))] : [];
  });
}

const count = (text, part) => text.split(part).length - 1;

// verifyPackageBackports throws when a file of `packages` does not carry each of its backports once: an installed Pi that was not
// patched (a reader would compare the port with the pinned release, not with the release plus its fixes) or was patched twice.
export function verifyPackageBackports(packages) {
  const problems = [];
  for (const file of packageBackportKeys()) {
    const [name, ...rest] = file.split("/");
    if (!(name in packages)) continue;
    const text = readFileSync(join(packages[name], packageLocation(name, rest)), "utf8");
    for (const [before, after] of patchesOf(file)) {
      // A backport that only removes text leaves nothing to count but the text it removed.
      const [part, want] = after === "" ? [before, 0] : [after, 1];
      const n = count(text, part);
      if (n !== want) problems.push(`${file} holds ${n} copies of a backport's ${after === "" ? "removed" : "added"} text, want ${want}: ${part.slice(0, 80)}`);
    }
  }
  if (problems.length > 0) throw new Error(problems.join("\n"));
}

const commonPrefix = (a, b) => {
  let n = 0;
  while (n < a.length && n < b.length && a[n] === b[n]) n++;
  return n;
};

// firstDifference says where the text of an entry (`before`) first differs from the file `text`: the file's line that shares the longest prefix with
// the entry's first line, then the first later line that differs, so a maintainer reads the change without diffing by hand.
function firstDifference(before, text) {
  const want = before.split("\n");
  const have = text.split("\n");
  let at = 0;
  let best = -1;
  have.forEach((line, i) => {
    const n = commonPrefix(want[0], line);
    if (n > best) [best, at] = [n, i];
  });
  const show = (line) => JSON.stringify((line ?? "<end of file>").slice(0, 140));
  for (let i = 0; i < want.length; i++) {
    if (have[at + i] !== want[i]) return `entry text line ${i + 1} reads ${show(want[i])}, the file's line ${at + i + 1} reads ${show(have[at + i])}`;
  }
  return `the entry's text occurs ${count(text, before)} times in the file`;
}

// classifyDependency says whether the Pi package installed at `root` still pins a carried dependency at the version the entry replaces ("pending"), pins the carried
// version ("carried": the target holds the commit and the entry must go), or pins anything else ("conflict" with its old pin gone, which the caller settles with the
// commit's ancestry: a target past the commit pins a later version).
function classifyDependency({ id, key, dependency: dep }, root) {
  const pinned = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).dependencies?.[dep.name];
  const state = pinned === dep.from ? "pending" : pinned === dep.to ? "carried" : "conflict";
  const detail = state === "conflict" ? `conflict: the package pins ${dep.name} ${pinned ?? "nowhere"}, the entry moves ${dep.from} to ${dep.to}` : "";
  return { id, key, state, beforeGone: state !== "pending", detail };
}

// classifyBackports says, for every block of a package in `packages`, whether the installed package is still the one the block was written for
// ("pending": each patch's before-text occurs once and its after-text not at all), already carries the block's change ("carried": each
// after-text occurs once, a removal's before-text not at all), or is neither ("conflict", with the file and the first differing line).
// Pi's own version of the change need not be the backport's text, so the caller decides a conflict whose before-text is gone (beforeGone) by
// asking whether the target contains the Pi commit.
export function classifyBackports(packages) {
  const results = [];
  // A mirror source is no file of an installed package: the compiled files of its commit decide the commit.
  for (const block of blocks.filter((block) => block.table !== "mirror")) {
    const { id, key, patches } = block;
    const [name, ...rest] = key.split("/");
    if (!(name in packages)) continue;
    if (block.dependency) {
      results.push(classifyDependency(block, packages[name]));
      continue;
    }
    const text = readFileSync(join(packages[name], packageLocation(name, rest)), "utf8");
    const states = patches.map(([before, after]) => {
      const nb = count(text, before);
      const na = after === "" ? 0 : count(text, after);
      if (after === "" ? nb === 0 : na === 1 && (nb === 0 || after.includes(before))) return "carried";
      if (nb === 1 && na === 0) return "pending";
      return `conflict: ${firstDifference(before, text)}`;
    });
    const bad = states.find((state) => state.startsWith("conflict"));
    const state = bad ? "conflict" : states.every((x) => x === "carried") ? "carried" : states.every((x) => x === "pending") ? "pending" : "conflict";
    // beforeGone: no patch's before-text is in the file any more. Pi's own version of a change rarely equals the backport's after-text
    // (the backport adds a note line), so a target that holds the Pi commit shows as "conflict" here with its before-text gone; the caller
    // settles that with the commit's ancestry.
    const beforeGone = patches.every(([before]) => count(text, before) === 0);
    results.push({ id, key, state, beforeGone, detail: bad ?? (state === "conflict" ? "some patches of the entry are carried and some are not" : "") });
  }
  return results;
}

// mirrorKey is the backport key of a file of the Pi repository the source mirror holds with the text of the installed package
// (packages/coding-agent/docs/x.md, packages/ai/src/providers/data/x.json), or undefined for a file no backport table covers.
const mirrorKey = (path) => {
  const match = /^packages\/coding-agent\/((?:docs|schemas)\/.+)$/.exec(path);
  if (match) return `pi-coding-agent/${match[1]}`;
  const data = /^packages\/ai\/src\/(providers\/data\/.+\.json)$/.exec(path);
  return data ? `pi-ai/${data[1]}` : undefined;
};
const mirrorPathOf = (key) => key.replace(/^pi-coding-agent\//, "packages/coding-agent/").replace(/^pi-ai\//, "packages/ai/src/");

// mirrorBackportPaths are the files of the source mirror a backport covers: those mirrorKey names, and the TypeScript sources of
// mirrorSourceBackports. Every other mirror file is the pinned build's, whose delta from the pin the sync ledger accounts for.
export const mirrorBackportPaths = () => [...packageBackportKeys().map(mirrorPathOf).filter((path) => mirrorKey(path) !== undefined && patchesOf(mirrorKey(path)).length > 0), ...Object.keys(mirrorSourceBackports)];

// readMirrorFile returns the text of `path` (relative to the Pi repository) in the source mirror `mirror` with the backports applied:
// the file of the pinned release plus the fixes the release line carries. The mirror itself stays the pinned release.
export function readMirrorFile(mirror, path) {
  let source = readFileSync(join(mirror, path), "utf8");
  for (const [before, after] of mirrorSourceBackports[path] ?? []) {
    if (source.split(before).length !== 2) throw new Error(`Pi source changed in ${path}, a post-v1.5.2 backport no longer applies: ${before}`);
    source = source.replace(before, () => after);
  }
  const key = mirrorKey(path);
  return key === undefined ? source : patchBackports(key, source);
}

// verifyMirrorBackports throws when a mirror file a backport covers is not the pinned release's (the backport no longer applies once).
export function verifyMirrorBackports(mirror) {
  for (const path of mirrorBackportPaths()) readMirrorFile(mirror, path);
}

const packagesOf = (args) => Object.fromEntries(args.map((arg) => arg.split(/=(.*)/s).slice(0, 2)));

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const [mode, ...args] = process.argv.slice(2);
  if (mode === "--packages") applyPackageBackports(packagesOf(args));
  else if (mode === "--package-files") process.stdout.write(JSON.stringify(packageBackportFiles(packagesOf(args))));
  else if (mode === "--verify-packages") verifyPackageBackports(packagesOf(args));
  else if (mode === "--classify") process.stdout.write(JSON.stringify(classifyBackports(packagesOf(args))));
  else if (mode === "--dependencies") process.stdout.write(JSON.stringify(carriedDependencies));
  else if (mode === "--read-mirror") process.stdout.write(readMirrorFile(args[0], args[1]));
  else if (mode === "--verify-mirror") verifyMirrorBackports(args[0]);
  else if (mode === "--mirror-paths") process.stdout.write(mirrorBackportPaths().map((path) => `${path}\n`).join(""));
  else applyBackportPatches(mode);
}
