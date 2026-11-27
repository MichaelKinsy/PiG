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
    let discardedOutput = false;
    const maxOutputBytes = DEFAULT_MAX_BYTES * 2;
    let tempFile;
    let totalBytes = 0;
    // Output after the command ended (from processes it left running) is not part of its result.
    let acceptingOutput = true;
    const ensureTempFile = () => {
        if (tempFile) {
            return;
        }
        tempFile = createOutputFileStream("pi-bash", ".log");
        for (const chunk of outputChunks) {
            tempFile.write(chunk);
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
        tempFile?.write(text);
        // Keep rolling buffer
        outputChunks.push(text);
        outputBytes += text.length;
        while (outputBytes > maxOutputBytes && outputChunks.length > 1) {
            const removed = outputChunks.shift();
            outputBytes -= removed.length;
            discardedOutput = true;
        }
        // Stream to callback
        if (options?.onChunk) {
            options.onChunk(text);
        }
    };
    const onData = (data) => {
        if (!acceptingOutput) {
            return;
        }
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
            acceptingOutput = false;
            await tempFile?.close();
            throw err;
        }
    }
    // The result is decided when the command ends: closing the file below takes time, and an abort or
    // output during it must not change the result.
    acceptingOutput = false;
    const cancelled = options?.signal?.aborted ?? false;
    flushOutput();
    const fullOutput = outputChunks.join("");
    const truncationResult = truncateTail(fullOutput);
    if (truncationResult.truncated) {
        ensureTempFile();
    }
    // A failed file is dropped: the result keeps the in-memory tail without a path.
    const saved = await tempFile?.close();
    const truncated = discardedOutput || truncationResult.truncated;
    return {
        output: truncationResult.truncated ? truncationResult.content : fullOutput,
        exitCode: cancelled ? undefined : (exitCode ?? undefined),
        cancelled,
        truncated,
        fullOutputPath: saved?.kind === "saved" ? saved.path : undefined,
        // The file opens on raw bytes, before ANSI codes and carriage returns are stripped, so it can fail
        // for output that turns out not to be truncated; nothing is missing then.
        ...(truncated && saved?.kind === "failed" ? { fullOutputError: saved.error } : {}),
    };
}
//# sourceMappingURL=bash-executor.js.map