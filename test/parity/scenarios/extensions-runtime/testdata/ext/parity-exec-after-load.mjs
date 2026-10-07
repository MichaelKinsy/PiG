// parity-exec-after-load: the factory makes no host call and returns; a callback it scheduled calls pi.exec on a later turn of the event loop, while PiG's runtime opens the connection it registers on. Pi's exec spawns the child whenever it is called (loader.ts:411-414). The tool_result handler reports the outcome through the bash result: "[parity-modified]" when the child printed what it should, the outcome otherwise.
export default function (pi) {
  let afterLoad = Promise.resolve("not-called");
  setImmediate(() => {
    afterLoad = pi.exec(process.execPath, ["-e", "process.stdout.write('after-load')"]).then(result => `resolved:${result.code}:${result.stdout}`, error => `rejected:${error.message}`);
  });
  pi.on("tool_result", async (event) => {
    if (event.toolName !== "bash") return;
    const outcome = await afterLoad;
    return { content: [{ type: "text", text: outcome === "resolved:0:after-load" ? "[parity-modified]" : outcome }] };
  });
}
