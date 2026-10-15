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
// The entry module does not await serve: Bun keeps a process whose entry module has a pending top-level await alive and never emits beforeExit, and the quit drain after RPC input ends depends on beforeExit (runtime.mjs listenForQuitDrain). A serve rejection ends the process with exit code 1, as a rejected top-level await does, whatever unhandledRejection listeners an extension added.
// served settles when the process has served every generation: the join a top-level await of serve would be.
export const served = generations.serve(process.stdin).catch(failServe);

function failServe(error) {
  console.error(error);
  process.exit(1);
}
