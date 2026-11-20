// The harness entry's supervisor: cli.mjs starts it, nothing else does. It starts the PiG binary, so the binary exists only as its child, and kills the
// binary when the IPC channel to the entry closes, which is how SIGKILL of the entry (no code runs) ends the binary. Run without that channel it ends
// at once and starts nothing.
import { spawn } from "node:child_process";
import { exitAfterStderr } from "../exit-after-stderr.mjs";
import { endLike, signals } from "./relay.mjs";

// A signal that reaches this process (the terminal's, which also reaches the entry and the binary) is ignored: the entry forwards its own over the channel.
const ignore = () => {};
for (const signal of signals) process.on(signal, ignore);
if (!process.connected) process.exit(1);
const binary = process.env.PIG_HARNESS_BINARY;
const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });
process.on("message", (message) => signals.includes(message?.signal) && child.kill(message.signal));
process.on("disconnect", () => child.kill("SIGKILL"));
child.on("error", (error) => exitAfterStderr(`${binary}: ${error.message}`));
child.on("exit", (code, signal) => endLike(code, signal, ignore));
