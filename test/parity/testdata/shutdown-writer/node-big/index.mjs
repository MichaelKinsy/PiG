// shutdown-writer/node-big: agent_end appends one 300000-character entry (a line larger than a pipe buffer, so a slow stdout reader leaves it queued behind the run's last lines); session_shutdown appends one more, which the JSON writer must not print.
export default function (pi) {
  pi.on("agent_end", () => {
    pi.appendEntry("big-entry", { text: "x".repeat(300000) });
  });
  pi.on("session_shutdown", () => {
    pi.appendEntry("shutdown-entry", { text: "from-shutdown" });
  });
}
