// Record the source package, integrity and hashes of generated runtime files.
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, relative } from "node:path";
import { identityPatches, patchIdentity } from "./pi-identity-patches.mjs";

const [shims, agent, output = join(shims, "vendor-manifest.json")] = process.argv.slice(2);
const shrinkwrap = JSON.parse(readFileSync(join(agent, "npm-shrinkwrap.json"), "utf8"));
const sdkLock = JSON.parse(readFileSync(new URL("../../extensions/sdk-ts/package-lock.json", import.meta.url), "utf8"));
const records = [];
const hash = file => createHash("sha256").update(readFileSync(file)).digest("hex");
function packageInfo(root) {
  const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  const lock = root === agent ? sdkLock.packages["node_modules/@earendil-works/pi-coding-agent"] : shrinkwrap.packages?.[relative(agent, root).split("\\").join("/")];
  return { name: manifest.name, version: manifest.version, integrity: lock?.integrity };
}
function files(root) {
  return readdirSync(root, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name, "en")).flatMap(entry => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}
function record(output, source, pkg, rewrite) {
  records.push({ path: relative(shims, output).split("\\").join("/"), package: packageInfo(pkg), source: source ? relative(pkg, source).split("\\").join("/") : undefined, sourceSha256: source ? hash(source) : undefined, sha256: hash(output), rewrite });
}
const piPackages = [
  ["pi-coding-agent", agent],
  ...["pi-ai", "pi-agent-core", "pi-tui", "pi-codemode", "pi-mcp"].map(name => [name, join(agent, "node_modules/@earendil-works", name)]),
];
for (const [name, pkg] of piPackages) {
  for (const output of files(join(shims, "pi-dist", name))) {
    const rel = relative(join(shims, "pi-dist", name), output).split("\\").join("/");
    if (rel.startsWith("sdk-bundle/")) {
      const generator = name === "pi-coding-agent" ? "bundle-pi-sdk.mjs" : "bundle-pi-libraries.mjs";
      record(output, undefined, pkg, `automation/gen/${generator}; exact input hashes in sdk-bundle/inputs.json`);
      continue;
    }
    let source = join(pkg, "dist", rel);
    if (name === "pi-tui" && rel.startsWith("native/")) source = join(pkg, rel);
    if (name === "pi-coding-agent" && (rel === "package.json" || rel === "README.md" || rel === "CHANGELOG.md" || rel.startsWith("docs/") || rel.startsWith("examples/"))) source = join(pkg, rel);
    if (!existsSync(source)) {
      if (name === "pi-coding-agent" && rel === "dist/index.js") {
        record(output, undefined, pkg, "absolute SDK entry -> owned session adapter");
        continue;
      }
      throw new Error(`No pinned source for ${output}`);
    }
    let rewrite;
    if (hash(source) !== hash(output)) {
      rewrite = name === "pi-coding-agent" && rel === "config.js" ? "D2 configuration and private asset paths; published bundled loader" : name === "pi-ai" && rel.startsWith("api/") ? "D74 host API leaf" : "module specifiers";
      // A file can carry both seams: keep the other rewrite's label unless the identity patches alone produce the output.
      const identity = "D26 PiG product identity (automation/gen/pi-identity-patches.mjs)";
      if (identityPatches[`${name}/${rel}`]) rewrite = patchIdentity(`${name}/${rel}`, readFileSync(source, "utf8")) === readFileSync(output, "utf8") ? identity : `${rewrite}; ${identity}`;
    }
    record(output, source, pkg, rewrite);
  }
}
function dependency(pkg, output) {
  const req = createRequire(join(pkg, "package.json"));
  for (const file of files(output)) {
    // Slash-separated on every platform, so nested node_modules are skipped on Windows too.
    const rel = relative(output, file).split("\\").join("/");
    if (rel.startsWith("node_modules/")) continue;
    record(file, join(pkg, rel), pkg);
  }
  const manifest = JSON.parse(readFileSync(join(pkg, "package.json"), "utf8"));
  for (const name of Object.keys(manifest.dependencies ?? {}).sort()) {
    const path = req.resolve.paths(name).map(root => join(root, name, "package.json")).find(existsSync);
    if (!path) throw new Error(`No locked package for ${name}`);
    dependency(dirname(path), join(output, "node_modules", name));
  }
}
for (const name of ["chalk", "undici", "semver", "minimatch", "hosted-git-info", "grok-mermaid", "proper-lockfile", "quickjs-wasi", "cross-spawn", "get-east-asian-width", "partial-json", "marked", "highlight.js", "ignore", "diff", "jiti"]) dependency(join(agent, "node_modules", name), join(shims, name));
const yaml = join(agent, "node_modules/yaml");
for (const output of files(join(shims, "yaml"))) {
  const rel = relative(join(shims, "yaml"), output);
  record(output, join(yaml, rel === "LICENSE" ? rel : "browser/" + rel), yaml);
}
const typebox = join(agent, "node_modules/typebox");
for (const entry of readdirSync(shims).filter(name => /^typebox.*\.mjs$/.test(name))) record(join(shims, entry), join(typebox, "package.json"), typebox, "automation/gen/vendor-typebox.sh esbuild bundle");
dependency(join(agent, "node_modules/@silvia-odwyer/photon-node"), join(shims, "photon-node"));
records.sort((a, b) => a.path.localeCompare(b.path, "en"));
writeFileSync(output, JSON.stringify({ generatedBy: "automation/gen/vendor-pi-dist.sh", files: records }, null, 2) + "\n");
