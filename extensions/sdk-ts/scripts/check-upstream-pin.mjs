import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../../..");
const packageJson = JSON.parse(await readFile(resolve(here, "../package.json"), "utf8"));
const packageLock = JSON.parse(await readFile(resolve(here, "../package-lock.json"), "utf8"));
const upstreamSource = await readFile(resolve(root, "coding/pigversion/pigversion.go"), "utf8");
const match = upstreamSource.match(/const UpstreamVersion = "([^"]+)"/);

if (!match) {
  throw new Error("cannot read coding.UpstreamVersion");
}

const expected = match[1];
const declared = packageJson.peerDependencies?.["@earendil-works/pi-coding-agent"];
const development = packageJson.devDependencies?.["@earendil-works/pi-coding-agent"];
const locked = packageLock.packages?.["node_modules/@earendil-works/pi-coding-agent"]?.version;

for (const [source, actual] of Object.entries({ declared, development, locked })) {
  if (actual !== expected) {
    throw new Error(`${source} Pi version ${JSON.stringify(actual)} does not match ${expected}`);
  }
}
