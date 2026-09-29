// Copy a pinned package and its production dependency tree without changing package resolution or bytes.
import { cpSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const [from, to] = process.argv.slice(2);
function copyPackage(manifestPath, destination) {
  const root = dirname(manifestPath);
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  mkdirSync(destination, { recursive: true });
  cpSync(root, destination, {
    recursive: true,
    filter: (path) => path !== join(root, "node_modules"),
  });
  const require = createRequire(manifestPath);
  for (const name of Object.keys(manifest.dependencies ?? {}).sort()) {
    const dependency = require.resolve.paths(name).map(path => join(path, name, "package.json")).find(existsSync);
    if (!dependency) throw new Error(`Missing locked dependency ${name} from ${manifestPath}`);
    copyPackage(dependency, join(destination, "node_modules", name));
  }
}
copyPackage(from, to);
