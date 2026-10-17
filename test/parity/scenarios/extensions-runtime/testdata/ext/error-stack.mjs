// Pi 1.2.0 runner.ts:1551-1557 reports a failed input handler with its stack and runs the next handler;
// interactive-mode.ts:3048-3061 (showExtensionError) shows the stack without its first line, each line trimmed, dimmed and
// indented (renderStack). The stack is fixed so both hosts show the same frames, including tabs, trailing spaces and an empty line.
export default function (pi) {
  pi.on("input", (event) => {
    if (event.text !== "stack-probe") return;
    const error = new Error("stack probe failed");
    error.stack = "Error: stack probe failed\n    at firstFrame (probe.mjs:1:1)\n\t\tat secondFrame (probe.mjs:2:2)   \n\n  stack-probe-last-frame";
    throw error;
  });
  pi.on("input", (event) => (event.text === "stack-probe" ? { action: "handled" } : undefined));
}
