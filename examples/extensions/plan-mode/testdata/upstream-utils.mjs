import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Import the pinned source directly; this utility module has no runtime dependencies.
const utils = await import(pathToFileURL(resolve(process.argv[2])).href);
const requests = JSON.parse(readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(requests.map(({ method, input }) => utils[method](input))));
