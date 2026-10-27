// Runs Pi's packages/codemode/src/cloudflare.ts at the pin and records what it does, for cloudflare_test.go.
//
// Regenerate from the repository root, with the Pi mirror installed (make upstream-mirror):
//   PI_UPSTREAM=.upstream/current node codemode/cloudflare/testdata/oracle.mjs > codemode/cloudflare/testdata/pi-golden.json
//
// cloudflare.ts imports `cloudflare:workers` and `quickjs-wasi/quickjs.wasm`, which exist only inside a Worker built by Wrangler:
// hooks.mjs supplies the same two things the Workers runtime and Wrangler's CompiledWasm rule do. Everything else is Pi's source.
import { readFileSync } from "node:fs";
import { createRequire, register } from "node:module";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const upstream = resolve(process.env.PI_UPSTREAM ?? ".upstream/current");
const src = `${upstream}/packages/codemode/src`;
process.env.PI_QUICKJS_WASM = createRequire(`${src}/cloudflare.ts`).resolve("quickjs-wasi/quickjs.wasm");
register("./hooks.mjs", import.meta.url);

const cf = await import(pathToFileURL(`${src}/cloudflare.ts`).href);
const pin = Object.fromEntries(
	readFileSync(`${upstream}/../pin`, "utf8").split("\n").filter((line) => line.includes("=") && !line.startsWith("#")).map((line) => line.split("=")),
);

const clone = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));

/** One execution on a fresh object: the frames the object sends, for a request that makes no tool call. */
async function execute(object, request) {
	const frames = [];
	await object.execute(request, async (messages) => {
		frames.push(clone(messages));
		return null;
	});
	return frames;
}

const data = (code, extra = {}) => ({ code, tools: [], globals: [], store: {}, ...extra });
const deterministic = { startedAt: 1_700_000_000_000, seed: 42 };
const loop = (n) => `let n = 0; for (let i = 0; i < ${n}; i++) n += i; return n`;
const oom = `const a = []; for (;;) a.push("x".repeat(1000) + a.length)`;

class Small extends cf.CodemodeSandboxDurableObject {
	maxInterruptBudget = 3;
}
class Medium extends cf.CodemodeSandboxDurableObject {
	maxInterruptBudget = 40;
}
class Tight extends cf.CodemodeSandboxDurableObject {
	maxMemoryLimitBytes = 2 * 1024 * 1024;
}
class Roomy extends cf.CodemodeSandboxDurableObject {
	maxMemoryLimitBytes = 64 * 1024 * 1024;
}

const objects = { default: () => new cf.CodemodeSandboxDurableObject({}, {}), small: () => new Small({}, {}), medium: () => new Medium({}, {}), tight: () => new Tight({}, {}), roomy: () => new Roomy({}, {}) };

const executes = [];
const add = async (name, object, request) => executes.push({ name, object, request, frames: await execute(objects[object](), request) });
for (const object of ["default", "small"]) {
	for (const n of [100, 10_000, 100_000]) await add(`${object} object runs a loop of ${n}`, object, { data: data(loop(n)) });
}
await add("the default object lets a request ask for more than nothing", "default", { data: data(loop(100)), interruptBudget: 1_000_000_000 });
await add("the medium object caps a request's budget", "medium", { data: data(loop(1_000_000)), interruptBudget: 1_000_000_000 });
await add("the medium object lets a request ask for less", "medium", { data: data(loop(1_000_000)), interruptBudget: 5 });
await add("the medium object runs a loop within its budget", "medium", { data: data(loop(100)) });
await add("the small object caps a request's budget", "small", { data: data(loop(1_000_000)), interruptBudget: 1_000_000_000 });
await add("the small object lets a request ask for less", "small", { data: data(loop(1_000_000)), interruptBudget: 1 });
for (const object of ["default", "tight", "roomy"]) {
	await add(`${object} object without a requested memory limit`, object, { data: data(oom) });
	await add(`${object} object with a requested memory limit of 256 MiB`, object, { data: data(oom, { memoryLimitBytes: 256 * 1024 * 1024 }) });
	await add(`${object} object with a requested memory limit of 1 MiB`, object, { data: data(oom, { memoryLimitBytes: 1024 * 1024 }) });
}
for (const object of ["default", "tight", "roomy"]) {
	await add(`${object} object fingerprint without a request`, object, { data: data("return 1", { deterministic }) });
	await add(`${object} object fingerprint with a memory request of 1 MiB`, object, { data: data("return 1", { deterministic, memoryLimitBytes: 1024 * 1024 }) });
	await add(`${object} object fingerprint with a memory request of 256 MiB and a budget of 77`, object, {
		data: data("return 1", { deterministic, memoryLimitBytes: 256 * 1024 * 1024 }),
		interruptBudget: 77,
	});
}

const objectsState = Object.fromEntries(
	Object.keys(objects).map((name) => {
		const object = objects[name]();
		return [name, { maxInterruptBudget: object.maxInterruptBudget, maxMemoryLimitBytes: object.maxMemoryLimitBytes }];
	}),
);

const result = (r) => ({
	ok: r.ok,
	...(r.ok ? { value: r.value === undefined ? { undefined: true } : r.value } : { error: r.error }),
	output: r.output,
	calls: r.calls.map((call) => ({ name: call.name, status: call.status })),
});
const inlines = [];
const inline = async (name, options, code) => {
	const sandbox = cf.createInlineCodemodeSandbox(options);
	try {
		inlines.push({ name, options: { interruptBudget: options.interruptBudget, memoryLimitBytes: options.memoryLimitBytes, tools: options.tools?.map((tool) => tool.name), wasm: options.wasm !== undefined }, code, result: result(await sandbox.execute(code)) });
	} finally {
		await sandbox.close();
	}
};
const echo = { name: "echo", execute: async (args) => ({ echoed: args ?? null }) };
await inline("an inline sandbox with the bundled module", { tools: [echo] }, `return await tools.echo({ a: 1 })`);
await inline("an inline sandbox with a budget", { interruptBudget: 25 }, loop(1_000_000));
await inline("an inline sandbox within its budget", { interruptBudget: 100_000 }, loop(100));
// Options type omits `wasm`, and the spread puts the bundled module last: a wasm option is ignored.
await inline("an inline sandbox ignores a wasm option", { wasm: "not a module" }, `return 1 + 1`);
await inline("an inline sandbox with a memory limit", { memoryLimitBytes: 2 * 1024 * 1024 }, oom);

process.stdout.write(
	JSON.stringify(
		{
			pin: pin.version,
			constants: { defaultRemoteMemoryLimitBytes: cf.DEFAULT_REMOTE_MEMORY_LIMIT_BYTES },
			quickjsWasmIsAModule: cf.cloudflareQuickJSWasm instanceof WebAssembly.Module,
			objects: objectsState,
			executes,
			inlines,
		},
		null,
		1,
	) + "\n",
);
