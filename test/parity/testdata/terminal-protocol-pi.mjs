import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { mock } from "node:test";

const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent", import.meta.url));
const version = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version;
if (version !== "0.99.2") throw new Error(`Expected Pi 0.99.2, found ${version}`);
const { ProcessTerminal } = await import(pathToFileURL(join(root, "node_modules/@earendil-works/pi-tui/dist/terminal.js")));
const lines = [];
for (const name of ["batch-order", "zero", "DA", "split", "late-confirmation", "rejected-prefix", "replay", "paste", "large-flags", "progress"]) {
    mock.timers.enable({ apis: ["setTimeout"] });
    const terminal = new ProcessTerminal();
    const previousWrite = process.stdout.write;
    const previousOn = process.stdin.on;
    let recording = false;
    let dataHandler;
    process.stdout.write = chunk => { if (recording) lines.push("write:" + String(chunk)); return true; };
    process.stdin.on = (event, listener) => { if (event === "data") dataHandler = listener; return process.stdin; };
    terminal.inputHandler = data => lines.push("input:" + data + ";kitty=" + terminal.kittyProtocolActive);
    try {
        terminal.queryAndEnableKittyProtocol();
        // The observation starts after decoder setup/query. Query and stop writes are covered by the exact original terminal tests, not this crop.
        recording = true;
        lines.push(name);
        const send = data => dataHandler(data);
        switch (name) {
            case "batch-order": send("a\x1b[?7u"); break;
            case "zero": send("\x1b[?0u"); send("\x1b[?62;4;52c"); break;
            case "DA": send("\x1b[?62;4;52c"); break;
            case "split": send("\x1b[?7"); mock.timers.tick(10); send("u"); break;
            case "late-confirmation": send("\x1b["); mock.timers.tick(50); lines.push("framing-timeout"); send("?7u"); break;
            case "rejected-prefix": send("\x1b["); mock.timers.tick(50); lines.push("framing-timeout"); send("a"); break;
            case "replay": send("\x1b["); mock.timers.tick(50); lines.push("framing-timeout"); mock.timers.tick(150); break;
            case "paste": send("\x1b["); mock.timers.tick(50); send("\x1b[200~\x1b[?7u\x1b[201~"); mock.timers.tick(150); break;
            case "large-flags": send("\x1b[?18446744073709551616u"); send("\x1b[?" + "9".repeat(400) + "u"); break;
            case "progress": terminal.setProgress(false); break;
        }
        lines.push("kitty=" + terminal.kittyProtocolActive);
        recording = false;
        terminal.stop();
        lines.push("closed-timer=" + (terminal.stdinBuffer === undefined && terminal.keyboardProtocolBufferFlushTimer === undefined));
    } finally {
        recording = false;
        terminal.stop();
        process.stdout.write = previousWrite;
        process.stdin.on = previousOn;
        mock.timers.reset();
    }
}
console.log(JSON.stringify(lines));
