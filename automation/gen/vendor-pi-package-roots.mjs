// Give the Pi packages the runtime serves from shim modules (pi-package-roots.mjs) a package root laid out like Pi's, so an
// extension that resolves one (import.meta.resolve), reads ../package.json beside its entry or computes dist/<file> from
// the package root finds what it finds in Pi's install. Run through vendor-pi-dist.sh.
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { packageRoots } from "./pi-package-roots.mjs";

const [shims, agent] = process.argv.slice(2);
// `export *` leaves out a default export, as the generated host peers' entries do (noderuntimepeers).
const exportsDefault = /^\s*export\s+default\b|\bas\s+default\s*[,}\n]/m;
for (const [name, entries] of Object.entries(packageRoots)) {
  const root = join(shims, "pi-dist", name);
  copyFileSync(join(agent, "node_modules", "@earendil-works", name, "package.json"), join(root, "package.json"));
  for (const [file, target] of Object.entries(entries)) {
    const output = join(root, file);
    mkdirSync(dirname(output), { recursive: true });
    const source = readFileSync(resolve(dirname(output), target), "utf8");
    // A bin's twin starts the program its target runs; an entry re-exports the module.
    let text = file === "dist/cli.js" ? `import ${JSON.stringify(target)};\n` : `export * from ${JSON.stringify(target)};\n`;
    if (file !== "dist/cli.js" && exportsDefault.test(source)) text += `export { default } from ${JSON.stringify(target)};\n`;
    writeFileSync(output, text);
  }
}
