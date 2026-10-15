/**
 * Bash command execution with streaming support and cancellation.
 *
 * This module provides a unified bash execution implementation used by:
 * - AgentSession.executeBash() for interactive and RPC modes
 * - Direct calls from modes that need bash execution
 */
import { splitIncompleteAnsiSuffix, stripAnsi } from "../utils/ansi.js";
import { createOutputFileStream } from "../utils/output-files.js";
import { sanitizeBinaryOutput } from "../utils/shell.js";
import { DEFAULT_MAX_BYTES, truncateTail } from "./tools/truncate.js";
// ============================================================================
// Implementation
// ============================================================================
/**
 * Execute a bash command using custom BashOperations.
 * Used for remote execution (SSH, containers, etc.).
 */
export async function executeBashWithOperations(command, cwd, operations, options) {
    const outputChunks = [];
    let outputBytes = 0;
    const maxOutputBytes = DEFAULT_MAX_BYTES * 2;
    let tempFilePath;
    let tempFileStream;
    let totalBytes = 0;
    const ensureTempFile = () => {
        if (tempFilePath) {
            return;
        }
        ({ path: tempFilePath, stream: tempFileStream } = createOutputFileStream("pi-bash", ".log"));
        for (const chunk of outputChunks) {
            tempFileStream.write(chunk);
        }
    };
    const decoder = new TextDecoder();
    // Unfinished escape sequence at the end of the previous chunk, completed by the next chunk.
    let pendingAnsi = "";
    const appendText = (rawText) => {
        // Sanitize: strip ANSI, replace binary garbage, normalize newlines
        const text = sanitizeBinaryOutput(stripAnsi(rawText)).replace(/\r/g, "");
        if (!text) {
            return;
        }
        // Start writing to temp file if exceeds threshold
        if (totalBytes > DEFAULT_MAX_BYTES) {
            ensureTempFile();
        }
        if (tempFileStream) {
            tempFileStream.write(text);
        }
        // Keep rolling buffer
        outputChunks.push(text);
        outputBytes += text.length;
        while (outputBytes > maxOutputBytes && outputChunks.length > 1) {
            const removed = outputChunks.shift();
            outputBytes -= removed.length;
        }
        // Stream to callback
        if (options?.onChunk) {
            options.onChunk(text);
        }
    };
    const onData = (data) => {
        totalBytes += data.length;
        const { complete, pending } = splitIncompleteAnsiSuffix(pendingAnsi + decoder.decode(data, { stream: true }));
        pendingAnsi = pending;
        appendText(complete);
    };
    const flushOutput = () => {
        const rest = pendingAnsi + decoder.decode();
        pendingAnsi = "";
        appendText(rest);
    };
    let exitCode = null;
    try {
        ({ exitCode } = await operations.exec(command, cwd, { onData, signal: options?.signal }));
    }
    catch (err) {
        // An aborted command still returns the output it produced so far
        if (!options?.signal?.aborted) {
            tempFileStream?.end();
            throw err;
        }
    }
    flushOutput();
    const fullOutput = outputChunks.join("");
    const truncationResult = truncateTail(fullOutput);
    if (truncationResult.truncated) {
        ensureTempFile();
    }
    tempFileStream?.end();
    const cancelled = options?.signal?.aborted ?? false;
    return {
        output: truncationResult.truncated ? truncationResult.content : fullOutput,
        exitCode: cancelled ? undefined : (exitCode ?? undefined),
        cancelled,
        truncated: truncationResult.truncated,
        fullOutputPath: tempFilePath,
    };
}
//# sourceMappingURL=bash-executor.js.map