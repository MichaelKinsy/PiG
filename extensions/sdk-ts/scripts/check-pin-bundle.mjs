// Fails unless pi-pin.d.ts holds the declarations of the pinned commit, by the commit its header names. Reads no installed Pi
// (make pin-check); `npm test` also regenerates the bundle from the installed pinned build and compares the bytes.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pinnedCommit, sdk } from "./pinned-commit.mjs";

const commit = pinnedCommit();
const named = readFileSync(join(sdk, "pi-pin.d.ts"), "utf8").slice(0, 2000).match(/ at commit ([0-9a-f]{40})/)?.[1];
if (named !== commit) {
  console.error(`extensions/sdk-ts/pi-pin.d.ts holds the declarations of Pi at commit ${named}; the pin is ${commit}: run make vendor-sdk-ts-pin-declarations`);
  process.exit(1);
}
