// Child process for manager_upstream_test.go's real-process cases (Pi packages/chord/test/manager.test.ts). The child
// host is PiG's Go host, so this Node entry only relays: forkDriver forks it with Node's IPC channel, and it runs the
// Go test binary (argv) as the worker with a JSON-lines pipe on fd 3, relaying messages both ways unchanged. It shares
// the worker's fate: the worker's exit code or signal is its own.
import { spawn } from "node:child_process";

const [command, ...args] = process.argv.slice(2);
const worker = spawn(command, args, { stdio: ["ignore", "inherit", "inherit", "pipe"], env: process.env });
const pipe = worker.stdio[3];
let buffered = "";
pipe.setEncoding("utf8");
pipe.on("data", (chunk) => {
	buffered += chunk;
	for (let end = buffered.indexOf("\n"); end >= 0; end = buffered.indexOf("\n")) {
		const line = buffered.slice(0, end);
		buffered = buffered.slice(end + 1);
		if (process.connected) process.send(JSON.parse(line));
	}
});
pipe.on("error", () => {});
process.on("message", (message) => pipe.write(`${JSON.stringify(message)}\n`));
worker.on("exit", (code, signal) => {
	if (signal !== null) process.kill(process.pid, signal);
	else process.exit(code ?? 1);
});
