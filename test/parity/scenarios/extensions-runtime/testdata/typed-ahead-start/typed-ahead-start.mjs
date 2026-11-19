// The first session_start of a run, across Node processes (a flag file the scenario's env names, under the run's own temp root), is held for 1.5 s: Pi runs it in its one process and PiG in the process of the session being replaced.
import fs from "node:fs";

export default function (pi) {
  pi.on("session_start", async () => {
    try {
      fs.writeFileSync(process.env.TYPED_AHEAD_FLAG, "1", { flag: "wx" });
    } catch {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 1500));
  });
}
