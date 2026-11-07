import { appendFileSync, readFileSync } from "node:fs";

// The first load (the server's startup build) returns at once. Every later load, a connection's build, appends to LISTEN_BARRIER_COUNT and then waits until two of them have started: the factory of the second connection's session returns only if the third session's extension process has started too. Builds that run one at a time never get there. LISTEN_BARRIER_RESULT receives "passed" when the barrier opened. pig additive (D232).
export default async function () {
  const counter = process.env.LISTEN_BARRIER_COUNT;
  let loads = 0;
  try { loads = readFileSync(counter, "utf8").length; } catch {}
  appendFileSync(counter, "x");
  if (loads === 0) return;
  for (;;) {
    let arrived = 0;
    try { arrived = readFileSync(counter, "utf8").length - 1; } catch {}
    if (arrived >= 2) break;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  appendFileSync(process.env.LISTEN_BARRIER_RESULT, "passed\n");
}
