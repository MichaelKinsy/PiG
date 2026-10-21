import { execFile } from "node:child_process";
import { promisify } from "node:util";

const run = promisify(execFile);

export default function (pi) {
  pi.on("model_select", async () => {
    setTimeout(() => pi.sendMessage({ customType: "probe", content: "from-timer", display: true }), 0);
    // Real I/O: the child process takes far longer than the timer, so Pi's loop turns and runs the timer before the handler resumes.
    await run(process.execPath, ["-e", "0"]);
  });
}
