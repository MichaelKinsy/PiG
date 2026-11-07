import { appendFileSync } from "node:fs";

// The extension's log, where the runtime writes its own diagnostics: a handler that threw, a host call that failed. The host forwards the process's stderr to the
// user's terminal, and Pi writes no stack for a throwing handler (print mode reports one line, interactive mode shows one), so a diagnostic of the runtime's own must not
// go there. The host names the process's log in PIG_EXT_LOG (the log its load and crash diagnostics read, which also receives everything the process writes to stderr),
// and the runtime appends one whole diagnostic per write. With no log, or one that cannot be written, the diagnostic goes to stderr, as it does when no host started the
// process. What the extension's own code writes (console.error) stays on stderr, as in Pi.

// The most the runtime writes to the log. The host bounds what a process writes to stderr, and a handler that throws on every event must not fill the disk through this
// path. The diagnostic that crosses the bound is the last.
export const DIAGNOSTIC_LOG_MAX = 1 << 20;

let written = 0;

// diagnose writes one diagnostic, which carries its own newline. It never throws: a diagnostic that cannot be written does not end the extension.
export function diagnose(text, path = process.env.PIG_EXT_LOG) {
  if (path) {
    const size = Buffer.byteLength(text);
    const before = written;
    written += size;
    if (before + size > DIAGNOSTIC_LOG_MAX) {
      if (before <= DIAGNOSTIC_LOG_MAX) {
        try { appendFileSync(path, "\n[... further SDK diagnostics dropped ...]\n"); } catch {}
      }
      return;
    }
    try {
      appendFileSync(path, text);
      return;
    } catch {}
  }
  try { process.stderr.write(text); } catch {}
}

// resetDiagnosticBytes is for tests: the bound counts per process.
export function resetDiagnosticBytes() {
  written = 0;
}
