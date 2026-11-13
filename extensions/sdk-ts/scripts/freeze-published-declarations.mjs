// Writes testdata/published-<version>.declarations.json.gz: the package.json fields and declaration files of the published npm packages
// the peer dependency names, each package verified against the sha512 integrity package-lock.json records, and the file's sha256.
// A run whose snapshot is already the peer dependency's version does nothing.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { PACKAGES } from "./declarations.mjs";

const sdk = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(readFileSync(join(sdk, "package.json"), "utf8"));
const lock = JSON.parse(readFileSync(join(sdk, "package-lock.json"), "utf8"));
const version = manifest.peerDependencies["@earendil-works/pi-coding-agent"];
const target = join(sdk, "testdata", `published-${version}.declarations.json.gz`);

if (existsSync(target) && existsSync(`${target}.sha256`)) {
  console.log(`${target} is the snapshot of ${version}`);
  process.exit(0);
}

function* declarations(directory) {
  for (const name of readdirSync(directory).sort()) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* declarations(path);
    else if (name.endsWith(".d.ts")) yield path;
  }
}

const work = mkdtempSync(join(tmpdir(), "pig-published-declarations-"));
try {
  const files = {};
  for (const name of PACKAGES) {
    const scoped = `@earendil-works/${name}`;
    const entry = lock.packages[`node_modules/${scoped}`] ?? lock.packages[`node_modules/@earendil-works/pi-coding-agent/node_modules/${scoped}`];
    if (!entry || entry.version !== version || !entry.integrity?.startsWith("sha512-")) throw new Error(`package-lock.json has no published ${scoped}@${version}`);
    const packed = spawnSync("npm", ["pack", `${scoped}@${version}`, "--pack-destination", work, "--ignore-scripts", "--silent"], { encoding: "utf8", shell: process.platform === "win32" });
    if (packed.status !== 0) throw new Error(`npm pack ${scoped}@${version}: ${packed.stderr}`);
    const tarball = join(work, packed.stdout.trim().split("\n").pop());
    const integrity = `sha512-${createHash("sha512").update(readFileSync(tarball)).digest("base64")}`;
    if (integrity !== entry.integrity) throw new Error(`${scoped}@${version}: the registry's tarball is ${integrity}, package-lock.json records ${entry.integrity}`);
    const directory = join(work, name);
    mkdirSync(directory);
    const extracted = spawnSync("tar", ["-xzf", tarball, "-C", directory, "--strip-components=1"], { encoding: "utf8" });
    if (extracted.status !== 0) throw new Error(`tar ${tarball}: ${extracted.stderr}`);
    const pkg = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
    const kept = { name: pkg.name, version: pkg.version, type: pkg.type, types: pkg.types, exports: pkg.exports };
    files[`${scoped}/package.json`] = JSON.stringify(kept);
    for (const path of declarations(join(directory, "dist"))) files[`${scoped}/${relative(directory, path).split("\\").join("/")}`] = readFileSync(path, "utf8");
  }
  const sorted = Object.fromEntries(Object.entries(files).sort(([x], [y]) => (x < y ? -1 : 1)));
  const bytes = gzipSync(Buffer.from(JSON.stringify({ version, files: sorted })), { level: 9 });
  for (const stale of readdirSync(join(sdk, "testdata")).filter((file) => file.startsWith("published-") && file.includes(".declarations.json.gz"))) rmSync(join(sdk, "testdata", stale));
  writeFileSync(target, bytes);
  writeFileSync(`${target}.sha256`, `${createHash("sha256").update(bytes).digest("hex")}\n`);
  console.log(`${target}: ${Object.keys(sorted).length} files, ${bytes.length} bytes`);
} finally {
  rmSync(work, { recursive: true, force: true });
}
