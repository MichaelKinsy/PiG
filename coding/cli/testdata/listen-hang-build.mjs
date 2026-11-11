import { appendFileSync, readFileSync, writeFileSync } from "node:fs";

// The first load (the server's startup build) returns. The second load, a connection's build, writes LISTEN_BUILD_MARKER and never returns, so the build is in flight until the process ends. pig additive (D232).
export default async function () {
  const counter = process.env.LISTEN_BUILD_COUNT;
  let loads = 0;
  try { loads = readFileSync(counter, "utf8").length; } catch {}
  appendFileSync(counter, "x");
  if (loads === 0) return;
  writeFileSync(process.env.LISTEN_BUILD_MARKER, "building");
  await new Promise(() => {});
}
