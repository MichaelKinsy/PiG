export default function (pi) {
  const trace = [];
  let release;
  pi.on("user_bash", async (_event, ctx) => {
    trace.push("hook:start");
    const pending = new Promise((resolve) => { release = resolve; });
    ctx.ui.notify("PT_BASH_WAIT", "info");
    await pending;
    trace.push("hook:end");
    ctx.ui.notify("PT_BASH_TRACE " + JSON.stringify(trace), "info");
    return { result: { output: "PT_BASH_DONE", exitCode: 7, cancelled: false, truncated: false } };
  });
  pi.registerCommand("bash-release", {
    description: "Release the awaiting user bash hook",
    handler: async () => {
      trace.push("release");
      release();
    },
  });
}
