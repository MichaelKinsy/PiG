// Node parent for manager_process_channel_test.go: it starts argv's command as a child with Node's IPC channel, the
// way child_process.fork does for a Node child (stdio "ipc": a socket pair on Unix, a named pipe passed as CRT
// descriptor 3 on Windows), so the Go child's ProcessChannel speaks to a real Node channel. Protocol: send the init
// control message; the child answers with its greeting and `ready`; then send a large message, which the child
// answers with its length; then send `shutdown`. It prints what it received and how the child ended as one JSON line.
import { spawn } from "node:child_process";

const [command, ...args] = process.argv.slice(2);
const child = spawn(command, args, { stdio: ["ignore", "inherit", "inherit", "ipc"], env: process.env });
const received = [];
const big = "x".repeat(300000);
child.on("message", (message) => {
	received.push(message);
	if (message?.chord2control?.type === "ready") child.send({ big });
	if (message?.bigLength !== undefined) child.send({ chord2control: { type: "shutdown" } });
});
child.send({ chord2control: { type: "init", value: { greeting: "hi" } } });
child.on("exit", (code, signal) => {
	console.log(JSON.stringify({ received, code, signal }));
	process.exit(0);
});
