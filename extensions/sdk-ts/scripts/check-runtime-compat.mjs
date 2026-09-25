import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, stat, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const sdkRoot = resolve(here, "..");
const repositoryRoot = resolve(sdkRoot, "../..");
const packageRoot = join(sdkRoot, "node_modules", "@earendil-works", "pi-coding-agent");
const toolsRoot = join(packageRoot, "dist", "core", "tools");
const shim = await import(pathToFileURL(join(repositoryRoot, "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pi-coding-agent.mjs")));
const published = await import(pathToFileURL(join(packageRoot, "dist", "index.js")));
const toolModules = Object.fromEntries(await Promise.all(
  ["bash", "edit", "find", "grep", "ls", "read", "write"].map(async (name) => [name, await import(pathToFileURL(join(toolsRoot, `${name}.js`)))]),
));

assert.equal(shim.VERSION, published.VERSION);

const factories = {
  bash: "createBashTool",
  edit: "createEditTool",
  find: "createFindTool",
  grep: "createGrepTool",
  ls: "createLsTool",
  read: "createReadTool",
  write: "createWriteTool",
};
for (const [name, factoryName] of Object.entries(factories)) {
  assert.equal(typeof shim[factoryName], "function", `${factoryName} export`);
  assert.deepEqual(
    JSON.parse(JSON.stringify(shim[factoryName]("/workspace").parameters)),
    JSON.parse(JSON.stringify(toolModules[name][factoryName]("/workspace").parameters)),
    `${factoryName} parameters`,
  );
}

async function compareResult(factoryName, moduleName, options, input) {
  const actual = await shim[factoryName]("/workspace", options.actual).execute("call", input);
  const expected = await toolModules[moduleName][factoryName]("/workspace", options.expected).execute("call", input);
  assert.deepEqual(actual, expected, `${factoryName} result`);
}

const readOperations = () => ({
  access: async () => {},
  readFile: async () => Buffer.from("one\ntwo\nthree"),
  detectImageMimeType: async () => null,
});
await compareResult("createReadTool", "read", { actual: { operations: readOperations() }, expected: { operations: readOperations() } }, { path: "sample.txt", offset: 2, limit: 1 });

function writeOperations(log) {
  return {
    mkdir: async (path) => { log.push(["mkdir", path]); },
    writeFile: async (path, content) => { log.push(["write", path, content]); },
  };
}
const actualWrites = [];
const expectedWrites = [];
await compareResult("createWriteTool", "write", {
  actual: { operations: writeOperations(actualWrites) },
  expected: { operations: writeOperations(expectedWrites) },
}, { path: "dir/file.txt", content: "value" });
assert.deepEqual(actualWrites, expectedWrites);

async function runEdit(factory, initial, input, prepare = false) {
  let content = initial;
  const operations = {
    access: async () => {},
    readFile: async () => Buffer.from(content),
    writeFile: async (_path, value) => { content = value; },
  };
  const tool = factory("/workspace", { operations });
  const params = prepare ? tool.prepareArguments(input) : input;
  try {
    return { result: await tool.execute("call", { path: "file.txt", ...params }), content };
  } catch (error) {
    return { error: error.message, content };
  }
}
const editCases = [
  ["a\nb\nc\n", { edits: [{ oldText: "b", newText: "B" }] }, false],
  ["one\ntwo\nthree", { edits: [{ oldText: "one\n", newText: "" }] }, false],
  ["keep  \n“smart”—x\ntail  \n", { edits: [{ oldText: '"smart"-x', newText: "changed" }] }, false],
  ["same\nsame\n", { edits: [{ oldText: "same", newText: "x" }] }, false],
  ["a\nb\nc\n", { oldText: "b", newText: "B" }, true],
  ["a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n", { edits: [{ oldText: "b", newText: "B" }, { oldText: "k", newText: "K" }] }, false],
];
for (const [initial, input, prepare] of editCases) {
  assert.deepEqual(
    await runEdit(shim.createEditTool, initial, input, prepare),
    await runEdit(toolModules.edit.createEditTool, initial, input, prepare),
    `edit ${JSON.stringify(input)}`,
  );
}

const findOperations = () => ({
  exists: async () => true,
  glob: async () => ["/workspace/b.ts", "/workspace/a.ts"],
});
await compareResult("createFindTool", "find", { actual: { operations: findOperations() }, expected: { operations: findOperations() } }, { pattern: "*.ts" });

const lsOperations = () => ({
  exists: async () => true,
  stat: async (path) => ({ isDirectory: () => !path.endsWith("a.txt") }),
  readdir: async () => ["z-dir", "a.txt"],
});
await compareResult("createLsTool", "ls", { actual: { operations: lsOperations() }, expected: { operations: lsOperations() } }, { path: "." });

function bashOperations() {
  return {
    exec: async (_command, _cwd, { onData }) => {
      onData(Buffer.from("first\n"));
      onData(Buffer.from("second\n"));
      return { exitCode: 0 };
    },
  };
}
await compareResult("createBashTool", "bash", { actual: { operations: bashOperations() }, expected: { operations: bashOperations() } }, { command: "ignored" });

const largeChunk = Buffer.alloc(8192, "x");
let updateCount = 0;
const largeBash = shim.createBashTool("/workspace", {
  operations: {
    exec: async (_command, _cwd, { onData }) => {
      for (let index = 0; index < 128; index++) onData(largeChunk);
      return { exitCode: 0 };
    },
  },
});
const largeResult = await largeBash.execute("call", { command: "ignored" }, undefined, () => { updateCount++; });
assert.equal(largeResult.details.truncation.totalBytes, 1024 * 1024);
assert.ok(Buffer.byteLength(largeResult.details.truncation.content) <= shim.DEFAULT_MAX_BYTES);
assert.ok(updateCount <= 3, `large bash emitted ${updateCount} updates`);
assert.equal((await stat(largeResult.details.fullOutputPath)).size, 1024 * 1024);
assert.equal((await readFile(largeResult.details.fullOutputPath)).length, 1024 * 1024);
await unlink(largeResult.details.fullOutputPath);

for (const [content, options] of [["one\ntwo\nthree", { maxLines: 2 }], ["ééé", { maxBytes: 3 }], ["short", {}]]) {
  const upstreamTruncate = await import(pathToFileURL(join(toolsRoot, "truncate.js")));
  assert.deepEqual(shim.truncateHead(content, options), upstreamTruncate.truncateHead(content, options));
  assert.deepEqual(shim.truncateTail(content, options), upstreamTruncate.truncateTail(content, options));
}

const runtimeModule = await import(pathToFileURL(join(repositoryRoot, "coding", "extension", "host", "subprocess", "runtime-node", "runtime.mjs")));
const sessionDir = await mkdtemp(join(tmpdir(), "pig-node-session-"));
try {
  const sessionPath = join(sessionDir, "session.jsonl");
  await writeFile(sessionPath, '{"type":"session","id":"s"}\n{"type":"message","id":"e1"}\n{"type":"message","id":"e2","parentId":"e1"}\n');
  const runtime = new runtimeModule.Runtime("fixture.mjs");
  runtime.state.session.sessionFile = sessionPath;
  let subscribedAt = -1;
  runtime.syncSessionLog = async (cursor) => { subscribedAt = cursor; };
  assert.deepEqual(runtime.ctx.sessionManager.getEntries().map((entry) => entry.id), ["e1", "e2"]);
  await runtime.sessionLogSync;
  assert.equal(subscribedAt, 2);
} finally {
  await rm(sessionDir, { recursive: true, force: true });
}

const queueDir = await mkdtemp(join(tmpdir(), "pig-node-queue-"));
try {
  const order = [];
  let releaseFirst;
  const firstGate = new Promise((resolveFirst) => { releaseFirst = resolveFirst; });
  const first = shim.withFileMutationQueue(join(queueDir, "file"), async () => {
    order.push("first-start");
    await firstGate;
    order.push("first-end");
  });
  const second = shim.withFileMutationQueue(join(queueDir, "file"), async () => { order.push("second"); });
  await new Promise((resolveTick) => setTimeout(resolveTick, 10));
  releaseFirst();
  await Promise.all([first, second]);
  assert.deepEqual(order, ["first-start", "first-end", "second"]);
} finally {
  await rm(queueDir, { recursive: true, force: true });
}

console.log("TypeScript runtime compatibility: pinned helpers and built-in factories match");
