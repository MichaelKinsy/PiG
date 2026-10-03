import net from "node:net";
import { resolve } from "node:path";
import { createInterface } from "node:readline";
import { importExtension } from "./jiti-loader.mjs";
import { invalidFactory, isHostCancellation, loadFailure, reportLoadFailure, Runtime } from "./runtime.mjs";
import { runWithRuntime } from "./state.mjs";

// Ports packages/coding-agent/src/core/extensions/loader.ts: extensionCache, useExtensionCacheCwd, loadExtensionModule.
// Pi re-invokes an extension's factory inside the process that already holds its module: /reload clears the factory cache (resource-loader.ts reload) and re-imports each module through jiti, while a session replacement keeps the cache for an unchanged cwd and calls the cached factory again. Module-level state therefore survives exactly as far as Pi's loader keeps the module.
// pig additive (D20): the host admits each generation over the process's stdin, one tab-separated line: admit, name, socket, entry, cwd, reload pass (0 outside a reload); name, socket, entry and cwd are percent-encoded so a name or path with a tab or line break survives; the generation registers on its own socket, so an older generation keeps serving until the host swaps and closes it.
// decodePath reverses the host's percent-encoding of the four characters that delimit a control line (packed_go.go encodeAdmissionField).
const pathEscapes = { "%25": "%", "%09": "\t", "%0A": "\n", "%0D": "\r" };
function decodePath(value) {
  return value?.replace(/%(?:25|09|0A|0D)/g, (escape) => pathEscapes[escape]);
}

export class Generations {
  // expected is the number of admissions the host owes a fresh process before it may go idle: a Node cell's manifest length.
  constructor(expected = 0) {
    this.expected = expected;
    this.admitted = 0;
    this.factories = new Map();
    this.cacheCwd = undefined;
    this.reloadPass = "0";
    this.nativeProviderObjects = new Map();
    this.providerConfigObjects = new Map();
    this.live = 0;
    this.loading = 0;
    this.loaded = 0;
    this.parked = false;
    this.running = new Set();
    this.close = () => {};
    this.input = undefined;
  }

  // The control channel keeps the event loop alive only while the process waits for an admission: before its manifest members have all arrived, and while parked for a replacement Session. Otherwise the runtime's own connections decide when the loop drains, which quit handling after RPC input ends depends on.
  holdInput() {
    if (this.parked || this.loading > 0 || this.admitted < this.expected) this.input?.ref?.();
    else this.input?.unref?.();
  }

  // Pi clears the cache on a cwd change, and once when a reload starts, before any module is looked up; the factories that reload loads stay cached (resource-loader.ts reload, loader.ts useExtensionCacheCwd). Every admission of one reload carries the same pass, so a later member does not evict an earlier member's factory.
  prepareCache({ reload = "0", cwd }) {
    const resolved = cwd ? resolve(cwd) : this.cacheCwd;
    const reloadStarts = reload !== "0" && reload !== this.reloadPass;
    if (reloadStarts || (this.cacheCwd !== undefined && resolved !== undefined && resolved !== this.cacheCwd)) this.factories.clear();
    if (reloadStarts) this.reloadPass = reload;
    this.cacheCwd = resolved;
  }

  async factoryFor(entry) {
    const cached = this.factories.get(entry);
    if (cached) return cached;
    const factory = await importExtension(entry);
    if (typeof factory !== "function") throw invalidFactory(entry);
    this.factories.set(entry, factory);
    return factory;
  }

  // admit runs one generation's factory and, when it succeeds, serves its connection until the host closes it. It resolves once the factory has finished, with whether the generation loaded.
  async admit(admission) {
    this.loading++;
    try {
      return await this.load(admission);
    } finally {
      this.loading--;
      this.admitted++;
      this.holdInput();
      this.settle();
    }
  }

  async load({ name, entry, socket, reload = "0", cwd }) {
    this.parked = false;
    process.env.PIG_EXT_NAME = name;
    this.prepareCache({ reload, cwd });
    const runtime = new Runtime(entry, this.nativeProviderObjects, this.providerConfigObjects, { name, socket });
    try {
      await runWithRuntime(runtime, async () => {
        const install = await this.factoryFor(entry);
        await install(runtime.api);
      });
    } catch (error) {
      runtime.discardLoad();
      if (runtime.earlyConn) await runtime.reportLoadFailureOnConnection(loadFailure(error));
      else await reportLoadFailure(socket, loadFailure(error));
      return false;
    }
    runtime.commitLoad();
    process.env.PIG_EXT_SOCKET = socket;
    this.live++;
    this.loaded++;
    const run = runWithRuntime(runtime, () => runtime.run()).catch((error) => {
      if (isHostCancellation(error)) return;
      console.error(`extension "${runtime.name}" runtime failed: ${error?.stack || error}`);
      process.exitCode = 1;
    }).finally(() => {
      this.running.delete(run);
      this.live--;
      this.settle();
    });
    this.running.add(run);
    return true;
  }

  // The process ends when every generation has ended and the host has not parked it for a replacement Session, as it did before the process outlived a factory call.
  settle() {
    if (this.live === 0 && this.loading === 0 && this.admitted >= this.expected && !this.parked) this.close();
  }

  // A park line names a socket the host listens on. Connecting to it tells the host that the process holds itself open, so the host may then retire its last generation.
  park(address) {
    this.parked = true;
    this.holdInput();
    if (!address) return Promise.resolve();
    return new Promise((resolve) => {
      const socket = net.createConnection(address, () => socket.destroy());
      socket.on("close", resolve);
      socket.on("error", resolve);
    });
  }

  // serve reads admissions until stdin closes. Loading is sequential: the host admits the next factory only after this one's register handshake.
  async serve(input = process.stdin) {
    const lines = createInterface({ input, crlfDelay: Infinity });
    this.close = () => { lines.close(); input.destroy?.(); };
    this.input = input;
    this.holdInput();
    for await (const line of lines) {
      const [op, encodedName, encodedSocket, encodedEntry, encodedCwd, reload] = line.split("\t");
      const name = decodePath(encodedName);
      const socket = decodePath(encodedSocket);
      const entry = decodePath(encodedEntry);
      const cwd = decodePath(encodedCwd);
      if (op === "park") {
        await this.park(socket);
        continue;
      }
      if (op === "admit") await this.admit({ name, entry, socket, cwd, reload: reload || "0" });
    }
    await Promise.all(this.running);
  }
}
