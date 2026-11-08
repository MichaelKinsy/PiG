
const outcomes = {};
const attempt = async (name, run) => {
	try {
		await run();
		outcomes[name] = "ok";
	} catch (error) {
		outcomes[name] = String(error.code ?? error.message);
	}
};
const port = Number(process.argv[2]);
(async () => {
	const tty = require("node:tty");
	const fs = require("node:fs");
	await attempt("a terminal descriptor", () => {
		const found = [];
		for (let fd = 0; fd < 256; fd++) if (tty.isatty(fd)) found.push(fd);
		if (found.length === 0) throw new Error("none");
	});
	await attempt("open /dev/tty", () => fs.openSync("/dev/tty", "r+"));
	await attempt("read a file", () => fs.readFileSync("/etc/hostname"));
	await attempt("list its directory", () => fs.readdirSync("."));
	await attempt("write a file", () => fs.writeFileSync("written", "x"));
	await attempt("node:sqlite", () => {
		if (process.getBuiltinModule("node:sqlite") === undefined) throw new Error("absent");
	});
	await attempt("require sqlite", () => require("node:sqlite"));
	await attempt("net.connect", () => new Promise((resolve, reject) => {
		const socket = require("node:net").connect(port, "127.0.0.1");
		socket.on("connect", () => (socket.destroy(), resolve()));
		socket.on("error", reject);
	}));
	await attempt("fetch", () => fetch("http://127.0.0.1:" + port + "/"));
	await attempt("a child process", () => require("node:child_process").spawnSync("true"));
	await attempt("a worker", () => new (require("node:worker_threads").Worker)("1", { eval: true }));
	await attempt("an addon", () => process.dlopen({ exports: {} }, "/nonexistent.node"));
	await attempt("the inspector", () => require("node:inspector").open(0));
	await attempt("an environment", () => {
		const names = Object.keys(process.env).filter((name) => name !== "LD_LIBRARY_PATH");
		if (names.length === 0) throw new Error("empty");
	});
	// Known gaps: signals to the user's processes, the machine's details, whether a path exists.
	await attempt("signal the launcher", () => process.kill(process.ppid, 0));
	await attempt("os details", () => require("node:os").userInfo().username);
	// Opus E13, a Node gap: module resolution tells whether a file exists (denied for one that does, not found for one
	// that does not).
	await attempt("whether a path exists", () => {
		const code = (path) => {
			try {
				require.resolve(path);
				return "resolved";
			} catch (error) {
				return error.code;
			}
		};
		if (code("/etc/hostname") === code("/etc/no-such-file-chord")) throw new Error("no difference");
	});
	process.stdout.write(JSON.stringify(outcomes));
})();
