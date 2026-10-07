// parity-load-exec: the factory runs a child with pi.exec while it loads, before any session binds (Pi's loader gives it an exec that spawns the child itself, loader.ts:411-414). The tool_result handler reports the outcome to the model through the result text, so a rejection (PiG's "extension connection closed or replaced") reads "not-modified" where a resolved child reads "modified:parity-modified".
export default async function (pi) {
  let outcome;
  try {
    const result = await pi.exec(process.execPath, ["-e", "process.stdout.write('load-exec-ok')"], { timeout: 10000 });
    outcome = result.stdout === "load-exec-ok" ? "[parity-modified]" : `exec-wrong-output:${result.stdout}`;
  } catch (error) {
    outcome = `exec-rejected:${error.message}`;
  }
  pi.on("tool_result", (event) => {
    if (event.toolName !== "bash") return;
    return { content: [{ type: "text", text: outcome }] };
  });
}
