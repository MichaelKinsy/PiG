import { adoptPiProcessIdentity } from "./process-identity.mjs";
import { Generations } from "./generations.mjs";

const entry = process.argv[2];
if (!entry) {
  console.error("usage: node cli.mjs <extension-entry>");
  process.exit(1);
}

adoptPiProcessIdentity();
// The first generation loads at start; later generations arrive on stdin, so /reload re-invokes the factory in this process.
const generations = new Generations();
// Pi keys its factory cache by the resolved configured cwd, which the host passes; process.cwd() is the physical path behind a symlink.
const cwd = process.env.PIG_EXT_CWD || process.cwd();
delete process.env.PIG_EXT_CWD;
const first = { name: process.env.PIG_EXT_NAME, entry, socket: process.env.PIG_EXT_SOCKET, cwd };
if (!(await generations.admit(first))) process.exit(1);
await generations.serve(process.stdin);
