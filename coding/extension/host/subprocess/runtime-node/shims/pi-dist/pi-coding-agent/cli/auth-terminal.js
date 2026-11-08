import { openBrowser } from "../utils/open-browser.js";
import { questionTerminal } from "../utils/terminal-question.js";
/** Terminal I/O on stdin and stdout. `onInterrupt` runs on Ctrl+C or EOF in a question, or Ctrl+C in a secret. */
export function createProcessLoginTerminal(onInterrupt) {
    return {
        interactive: process.stdin.isTTY === true && process.stdout.isTTY === true,
        write: (line) => process.stdout.write(`${line}\n`),
        error: (line) => process.stderr.write(`${line}\n`),
        question: (message, signal) => questionTerminal(message, { signal, output: process.stdout, onInterrupt }),
        secret: (message, signal) => readSecret(message, signal, onInterrupt),
        openUrl: openBrowser,
    };
}
function readSecret(message, signal, onInterrupt) {
    const stdin = process.stdin;
    return new Promise((resolve, reject) => {
        if (signal.aborted) {
            reject(new Error("Login cancelled"));
            return;
        }
        let value = "";
        const finish = () => {
            stdin.off("data", onData);
            signal.removeEventListener("abort", onAbort);
            stdin.setRawMode(false);
            stdin.pause();
            process.stdout.write("\n");
        };
        const onAbort = () => {
            finish();
            reject(new Error("Login cancelled"));
        };
        const onData = (data) => {
            const chunk = data.toString("utf8");
            // Ignore escape sequences such as arrow keys.
            if (chunk.startsWith("\u001b"))
                return;
            for (const char of chunk) {
                if (char === "\r" || char === "\n") {
                    finish();
                    resolve(value);
                    return;
                }
                if (char === "\u0003") {
                    onInterrupt();
                    return;
                }
                if (char === "\u007f" || char === "\b") {
                    value = Array.from(value).slice(0, -1).join("");
                }
                else if (char >= " ") {
                    value += char;
                }
            }
        };
        process.stdout.write(message);
        stdin.setRawMode(true);
        stdin.resume();
        stdin.on("data", onData);
        signal.addEventListener("abort", onAbort, { once: true });
    });
}
//# sourceMappingURL=auth-terminal.js.map