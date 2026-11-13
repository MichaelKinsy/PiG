// The two builds of Pi's declarations this package reads: the Pi installed in node_modules (the pinned source build, the oracle) and the
// published release the peer dependency names (testdata/published-<version>.declarations.json.gz, checksum-verified, not the network).
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, realpathSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { gunzipSync } from "node:zlib";
import { PACKAGES } from "./declarations.mjs";
import { pinnedCommit, sdk } from "./pinned-commit.mjs";

export { pinnedCommit, sdk };

// The declaration files of a directory, and the JSON files, which a declaration file may import for its type.
function* declarations(directory) {
  for (const name of readdirSync(directory).sort()) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* declarations(path);
    else if (name.endsWith(".d.ts") || name.endsWith(".json")) yield path;
  }
}

// installedTree reads the installed Pi: its tree, the node_modules directory, and each package's directory and full manifest.
export function installedTree() {
  const modules = realpathSync(join(sdk, "node_modules"));
  const agent = join(modules, "@earendil-works", "pi-coding-agent");
  const tree = new Map();
  const packages = new Map();
  for (const name of PACKAGES) {
    const candidates = [join(modules, "@earendil-works", name), join(agent, "node_modules", "@earendil-works", name)];
    const directory = candidates.find((candidate) => existsSync(join(candidate, "package.json")));
    assert.ok(directory, `no installed @earendil-works/${name} under ${modules}`);
    const pkg = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
    packages.set(`@earendil-works/${name}`, { directory, manifest: pkg });
    tree.set(`@earendil-works/${name}/package.json`, JSON.stringify({ name: pkg.name, version: pkg.version, type: pkg.type, types: pkg.types, exports: pkg.exports }));
    for (const path of declarations(join(directory, "dist"))) {
      tree.set(`@earendil-works/${name}/${path.slice(directory.length + 1).split("\\").join("/")}`, readFileSync(path, "utf8"));
    }
  }
  return { tree, modules, packages };
}

// published says whether the installed Pi is the published release of a tag pin (PIG_PARITY_PI_PUBLISHED=1) and not a source build.
export const published = process.env.PIG_PARITY_PI_PUBLISHED === "1";

// pinnedBuild is installedTree once the installed Pi is known to be the build of the pinned commit.
export function pinnedBuild() {
  const installed = installedTree();
  const commit = pinnedCommit();
  const stamp = join(dirname(installed.modules), ".pig-pi-oracle.json");
  if (!published) {
    assert.ok(existsSync(stamp), `${installed.modules} is not a pinned source build (no ${stamp}); run make parity-deps without PIG_PARITY_PI_PUBLISHED`);
    assert.equal(JSON.parse(readFileSync(stamp, "utf8")).commit, commit, `${installed.modules} is not the build of the pinned commit ${commit}`);
  }
  return { ...installed, commit };
}

// publishedTree reads the snapshot of the release the peer dependency names: { version, tree }.
export function publishedTree() {
  const manifest = JSON.parse(readFileSync(join(sdk, "package.json"), "utf8"));
  const version = manifest.peerDependencies["@earendil-works/pi-coding-agent"];
  const file = join(sdk, "testdata", `published-${version}.declarations.json.gz`);
  assert.ok(existsSync(file), `no snapshot of Pi ${version}: run node scripts/freeze-published-declarations.mjs (make vendor-sdk-ts-declarations)`);
  const bytes = readFileSync(file);
  assert.equal(createHash("sha256").update(bytes).digest("hex"), readFileSync(`${file}.sha256`, "utf8").trim(), `${file} does not match its recorded sha256`);
  const snapshot = JSON.parse(gunzipSync(bytes).toString("utf8"));
  assert.equal(snapshot.version, version, `${file} holds Pi ${snapshot.version}`);
  return { version, tree: new Map(Object.entries(snapshot.files)) };
}
