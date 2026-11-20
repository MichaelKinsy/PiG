import fs from "node:fs";

// Records the events of a turn that the quit aborts and, from its agent_settled handler, sends the user message "second" once. session_shutdown runs after the TUI has stopped, so it
// reports the record on the controlling terminal, which Pi's process and PiG's extension process share: the record shows whether the message started a turn, and whether the
// quit waited for it (session_shutdown is the last line).
export default function (pi) {
  const lines = [];
  let sent = false;
  const text = (message) => (Array.isArray(message.content) ? message.content : []).filter((block) => block.type === "text").map((block) => block.text).join("").slice(0, 24).replaceAll("\n", "\\n");
  pi.on("message_end", (event) => {
    const message = event.message;
    if (message.role === "user" || message.role === "assistant") lines.push(`message_end ${message.role} ${message.stopReason ?? ""} ${text(message)}`);
  });
  pi.on("agent_end", () => { lines.push("agent_end"); });
  pi.on("agent_settled", (event) => {
    lines.push(`agent_settled aborted=${event.aborted}`);
    if (sent) return;
    sent = true;
    pi.sendUserMessage("second");
    lines.push("sendUserMessage second");
  });
  pi.on("session_shutdown", (event) => {
    lines.push(`session_shutdown ${event.reason}`);
    fs.writeFileSync("/dev/tty", `QUIT_SETTLED_BEGIN\n${lines.join("\n")}\nQUIT_SETTLED_END\n`);
  });
}
