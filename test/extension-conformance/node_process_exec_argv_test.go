package extensionconformance

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi runs an extension inside its own Node process (core/extensions/loader.ts loads it with jiti), so what the extension reads from `process` and what a child it starts inherits are what the user started Node with: process.execArgv is [] and a child forked with the default or with `[...process.execArgv, ...]` starts bare. PiG starts a Node extension with its runtime's loader registered, and that registration must not show in process.execArgv or in what a child or a worker thread inherits (issue #517: pi-extensible-workflows forks its worker with process.execArgv plus --permission, and a child that inherits `--import <PiG's register-loader.mjs>` fails with ERR_ACCESS_DENIED before it starts).
//
// Each value below is one no default produces: the exit code 7 and the output only the fixture's child script writes, so a row that never starts the child cannot pass.
const nodeExecArgvFixture = nodeAPIPrelude + `
import { fork, spawn } from "node:child_process";
import { mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { Worker } from "node:worker_threads";

const permission = process.allowedNodeEnvironmentFlags.has("--permission") ? "--permission" : "--experimental-permission";
const scratch = () => {
  const dir = realpathSync(mkdtempSync(join(tmpdir(), "pig-exec-argv-")));
  writeFileSync(join(dir, "child.cjs"), "process.stdout.write('child:' + JSON.stringify(process.execArgv.filter((flag) => !flag.startsWith('--allow-fs-read') && !flag.includes('permission')))); process.exit(7);");
  writeFileSync(join(dir, "worker.cjs"), "require('node:worker_threads').parentPort.postMessage('worker:' + JSON.stringify(process.execArgv));");
  return dir;
};
const finish = (child) => new Promise((resolve) => {
  let out = "", err = "";
  child.stdout?.on("data", (data) => (out += data));
  child.stderr?.on("data", (data) => (err += data));
  child.on("exit", (code) => resolve([code, out, err.split("\n").find((line) => line.startsWith("Error")) ?? ""].join(",")));
});

export default function (pi) {
  pi.registerCommand("execargv", { handler: async (_args, ctx) => {
    lines(ctx, "execArgv", JSON.stringify(process.execArgv), "NODE_OPTIONS", process.env.NODE_OPTIONS);
  } });
  pi.registerCommand("fork", { handler: async (_args, ctx) => {
    const dir = scratch();
    lines(ctx, "fork", await finish(fork(join(dir, "child.cjs"), [], { stdio: ["ignore", "pipe", "pipe", "ipc"] })));
  } });
  // The issue's repro: execArgv inherited, then a permission model that reads only the child's directory.
  pi.registerCommand("fork-permission", { handler: async (_args, ctx) => {
    const dir = scratch();
    lines(ctx, "fork-permission", await finish(fork(join(dir, "child.cjs"), [], { execArgv: [...process.execArgv, permission, "--allow-fs-read=" + dir], stdio: ["ignore", "pipe", "pipe", "ipc"] })));
  } });
  pi.registerCommand("spawn", { handler: async (_args, ctx) => {
    const dir = scratch();
    lines(ctx, "spawn", await finish(spawn(process.execPath, [...process.execArgv, permission, "--allow-fs-read=" + dir, join(dir, "child.cjs")], { stdio: ["ignore", "pipe", "pipe"] })));
  } });
  pi.registerCommand("worker", { handler: async (_args, ctx) => {
    const dir = scratch();
    const message = await new Promise((resolve) => {
      const worker = new Worker(join(dir, "worker.cjs"));
      worker.on("message", resolve);
      worker.on("error", (error) => resolve("error:" + (error.code ?? error.message)));
    });
    lines(ctx, "worker", message);
  } });
}
`

func TestNodeExtensionProcessExecArgvIsWhatTheUserStartedNodeWith(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		// NODE_OPTIONS is the user's, and the runtime adds nothing to it.
		t.Setenv("NODE_OPTIONS", "--stack-trace-limit=7")
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-execargv", nodeExecArgvFixture})
		for _, row := range []struct{ command, want string }{
			{"execargv", `execArgv|[]|NODE_OPTIONS|--stack-trace-limit=7`},
			{"fork", `fork|7,child:[],`},
			{"fork-permission", `fork-permission|7,child:[],`},
			{"spawn", `spawn|7,child:[],`},
			{"worker", `worker|worker:[]`},
		} {
			command, want := row.command, row.want
			t.Run(command, func(t *testing.T) {
				rig.command("nodeapi-execargv", command, "")
				if !awaitCondition(testbudget.Context(t), func() bool { return slices.Contains(rig.notifications(), want) }) {
					t.Fatalf("notifications %q, want %q", rig.notifications(), want)
				}
			})
		}
	})
}
