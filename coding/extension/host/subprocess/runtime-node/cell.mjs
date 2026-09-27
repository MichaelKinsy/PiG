import { readFile } from "node:fs/promises";
import net from "node:net";
import { pathToFileURL } from "node:url";
import { Runtime } from "./runtime.mjs";
import { runWithRuntime } from "./state.mjs";

// A failed factory connects and closes without registering so the host reports that member's load failure instead of waiting for a connection that will never arrive.
function signalLoadFailure(sockPath) {
  return new Promise((resolve) => {
    if (!sockPath) {
      resolve();
      return;
    }
    // destroy(), not end(): an end() only half-closes (sends FIN) and Node's
    // "close" event does not fire until the peer closes its side too. The
    // host has not called Accept() on this socket yet (it is still working
    // through earlier members), so waiting for "close" here would hang this
    // whole loop, and with it every member still waiting for its own turn.
    // destroy() tears the connection down unilaterally, so this always
    // settles regardless of when, or whether, the host ever accepts it.
    const socket = net.createConnection(sockPath, () => socket.destroy());
    socket.on("error", () => resolve());
    socket.on("close", () => resolve());
  });
}

// Each member keeps its own socket and registration. Factories install serially in plan order, matching Pi's loader.ts loadExtensionsInternal. A failed factory is reported without stopping healthy members. Successfully installed runtimes then run concurrently until all members stop.
const manifestPath = process.argv[2];
if (!manifestPath) {
  console.error("usage: node cell.mjs <manifest.json>");
  process.exit(1);
}

const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
if (!Array.isArray(manifest) || manifest.length === 0) {
  console.error("node cell manifest is empty");
  process.exit(1);
}

const running = [];
for (const member of manifest) {
  // The Runtime constructor reads PIG_EXT_NAME synchronously, so setting it
  // per member here is safe. PIG_EXT_SOCKET is read later, inside connect(),
  // which this file calls only from the second loop below (immediately
  // before each member's own turn), never here: setting it in this loop
  // would have every member's later, concurrent connect() read whatever
  // member happened to run last.
  process.env.PIG_EXT_NAME = member.name;
  const runtime = new Runtime(member.entry);
  try {
    await runWithRuntime(runtime, async () => {
      const mod = await import(pathToFileURL(member.entry).href);
      const install = mod?.default ?? mod;
      if (typeof install !== "function") {
        throw new Error(`Extension does not export a valid factory function: ${member.entry}`);
      }
      await install(runtime.api);
    });
  } catch (err) {
    console.error(`extension "${member.name}" failed to load: ${err?.stack || err}`);
    await signalLoadFailure(process.env[member.sockEnv]);
    continue;
  }
  running.push({ runtime, sockEnv: member.sockEnv });
}

if (running.length === 0) {
  process.exit(1);
}

// Runtime.connect() reads PIG_EXT_SOCKET before its first await, so each call captures its own socket before the next member starts. Report each rejection immediately, retain a failing exit status, and join healthy siblings without terminating them.
await Promise.all(
  running.map(({ runtime, sockEnv }) => {
    process.env.PIG_EXT_SOCKET = process.env[sockEnv] || "";
    return runWithRuntime(runtime, () => runtime.run()).catch((err) => {
      console.error(`extension "${runtime.name}" runtime failed: ${err?.stack || err}`);
      process.exitCode = 1;
    });
  }),
);
