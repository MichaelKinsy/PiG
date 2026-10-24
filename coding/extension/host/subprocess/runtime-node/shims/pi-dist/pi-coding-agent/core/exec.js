/**
 * Shared command execution utilities for extensions and custom tools.
 */
import { spawn } from "node:child_process";
import { StringDecoder } from "node:string_decoder";
import { waitForChildProcess } from "../utils/child-process.js";
/**
 * Execute a shell command and return stdout/stderr/code.
 * Supports timeout and abort signal.
 */
export async function execCommand(command, args, cwd, options) {
    return new Promise((resolve) => {
        const proc = spawn(command, args, {
            cwd,
            shell: false,
            stdio: ["ignore", "pipe", "pipe"],
        });
        let stdout = "";
        let stderr = "";
        // Decode across chunks: a multi-byte UTF-8 character can be split between two chunks,
        // and decoding each chunk separately would corrupt it (earendil-works/pi#11312).
        const stdoutDecoder = new StringDecoder("utf8");
        const stderrDecoder = new StringDecoder("utf8");
        let killed = false;
        let timeoutId;
        const killProcess = () => {
            if (!killed) {
                killed = true;
                proc.kill("SIGTERM");
                // Force kill after 5 seconds if SIGTERM doesn't work
                setTimeout(() => {
                    if (!proc.killed) {
                        proc.kill("SIGKILL");
                    }
                }, 5000);
            }
        };
        // Handle abort signal
        if (options?.signal) {
            if (options.signal.aborted) {
                killProcess();
            }
            else {
                options.signal.addEventListener("abort", killProcess, { once: true });
            }
        }
        // Handle timeout
        if (options?.timeout && options.timeout > 0) {
            timeoutId = setTimeout(() => {
                killProcess();
            }, options.timeout);
        }
        proc.stdout?.on("data", (data) => {
            stdout += stdoutDecoder.write(data);
        });
        proc.stderr?.on("data", (data) => {
            stderr += stderrDecoder.write(data);
        });
        // Wait for process termination without hanging on inherited stdio handles
        // held open by detached descendants.
        waitForChildProcess(proc)
            .then((code) => {
            if (timeoutId)
                clearTimeout(timeoutId);
            stdout += stdoutDecoder.end();
            stderr += stderrDecoder.end();
            if (options?.signal) {
                options.signal.removeEventListener("abort", killProcess);
            }
            resolve({ stdout, stderr, code: code ?? 0, killed });
        })
            .catch((error) => {
            if (timeoutId)
                clearTimeout(timeoutId);
            stdout += stdoutDecoder.end();
            stderr += stderrDecoder.end();
            if (options?.signal) {
                options.signal.removeEventListener("abort", killProcess);
            }
            resolve({ stdout, stderr: stderr || error.message, code: 1, killed });
        });
    });
}
//# sourceMappingURL=exec.js.map