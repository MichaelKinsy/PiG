// user-bash.mjs routes user_bash by the command text so one extension covers
// every upstream #9068 RPC case.
export default function userBashExtension(pi) {
  pi.on("user_bash", async (event) => {
    if (event.command.includes("THROW")) throw new Error("Routing failed");
    if (event.command.includes("EMPTY")) return {};
    if (event.command.includes("RESULT")) {
      return { result: { output: `handled:${event.cwd === process.cwd() ? "cwd" : event.cwd}`, exitCode: 0, cancelled: false, truncated: false } };
    }
    return undefined;
  });
}
