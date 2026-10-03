import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { generateGoStubs, goName, readHeader, writeOutputs } from "../src/go-stubs.mjs";
import { scratchDir } from "./scratch.mjs";

const cli = fileURLToPath(new URL("../src/gen-go-stubs.mjs", import.meta.url));

function write(root, name, text) {
  const file = path.join(root, name);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, text);
}

// fixture builds a one-package upstream tree and an empty Go module.
function fixture(t) {
  const root = scratchDir("pig-go-stubs-");
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const upstream = path.join(root, "upstream");
  write(upstream, "packages/demo/package.json", JSON.stringify({ name: "@x/demo", exports: { ".": { types: "./dist/index.d.ts" } } }));
  write(upstream, "packages/demo/src/index.ts", [
    'export { DEFAULT_LIMIT, Codec, CodecError, encode, limit, type Bye, type Frame, type Hello, type Limits, type Message, type Mode, type Options, type Sink } from "./codec.ts";',
    "",
  ].join("\n"));
  write(upstream, "packages/demo/src/codec.ts", [
    "export const DEFAULT_LIMIT = 16 * 1024;",
    'export type Mode = "fast" | "safe";',
    "export interface Options { limit?: number; mode: Mode; onError?: (error: Error) => void }",
    "export interface Sink { write(chunk: Uint8Array): Promise<void>; close(): void }",
    'export type Message = { type: "a"; id: string } | { type: "b"; id: string; payload?: unknown };',
    'export type Hello = { type: "hello"; version: number };',
    'export type Bye = { type: "bye" };',
    "export type Frame = Hello | Bye;",
    "export interface Limits { max?: number }",
    "export function limit(limits?: Limits): number { return limits?.max ?? 0; }",
    "export class CodecError extends Error {",
    "  readonly code: string;",
    '  constructor(code: string) { super(code); this.name = "CodecError"; this.code = code; }',
    "}",
    "function check(value: number): number { if (value < 0) throw new CodecError(\"negative\"); return value; }",
    "/** Encodes one message. */",
    "export function encode(message: Message, options?: Options): Uint8Array { check(1); return new Uint8Array(); }",
    "export class Codec {",
    "  #state = 0;",
    "  constructor(options: Options) {}",
    "  get state(): number { return this.#state; }",
    "  async flush(signal?: AbortSignal): Promise<number> { return 0; }",
    "  push(chunk: Uint8Array): Message[] { check(chunk.length); return []; }",
    "}",
    "",
  ].join("\n"));
  write(upstream, "packages/demo/test/codec.test.ts", [
    'import { describe, it } from "vitest";',
    'describe("codec", () => {',
    '  it("encodes", () => {});',
    '  it("rejects negative limits", () => {});',
    "});",
    'it("standalone", () => {});',
    "",
  ].join("\n"));
  const goDir = path.join(root, "go", "demo");
  write(root, "go/go.mod", "module fixture\n\ngo 1.26\n");
  fs.mkdirSync(goDir, { recursive: true });
  return { root, upstream, goDir };
}

function run(context, ...extra) {
  return spawnSync(process.execPath, [cli, "--source-root", context.upstream, "--package", "demo", "--out", context.goDir, "--go-package", "demo", "--repo-root", path.join(context.root, "go"), ...extra], { encoding: "utf8" });
}

function goVet(context) {
  const result = spawnSync("go", ["vet", "./..."], { cwd: path.join(context.root, "go"), encoding: "utf8", env: { ...process.env, GOFLAGS: "-mod=mod", GOWORK: "off" } });
  assert.equal(result.status, 0, result.stderr);
}

test("goName keeps Pi's spelling and turns SCREAMING_SNAKE into PascalCase", () => {
  assert.equal(goName("PROTOCOL_VERSION"), "ProtocolVersion");
  assert.equal(goName("isServerId"), "IsServerId");
  assert.equal(goName("ServerId"), "ServerId");
  assert.equal(goName("#private"), "Private");
});

test("gen-go-stubs writes compiling stubs and test skeletons, and is idempotent", (t) => {
  const context = fixture(t);
  const first = run(context);
  assert.equal(first.status, 0, first.stderr);
  const stub = fs.readFileSync(path.join(context.goDir, "codec_stub.go"), "utf8");
  assert.ok(readHeader(stub).valid);
  assert.match(stub, /const DefaultLimit = 16 \* 1024/);
  assert.match(stub, /type Mode string/);
  assert.match(stub, /ModeFast Mode = "fast"/);
  // Data interfaces are structs; method-only interfaces stay Go interfaces.
  assert.match(stub, /type Options struct \{\n\tLimit\s+\*float64\s+`json:"limit,omitempty"`/);
  assert.match(stub, /type Sink interface \{\n\tWrite\(ctx context\.Context, chunk \[\]byte\) error\n\tClose\(\)\n\}/);
  // Anonymous variants sharing a discriminant merge into one struct.
  assert.match(stub, /type Message struct \{[^}]*Payload any\s+`json:"payload,omitempty"`/s);
  assert.match(stub, /type CodecError struct \{\n\tMessage string\n\tCode\s+string\n\}/);
  // A function that reaches a throwing helper returns an error.
  assert.match(stub, /\/\/ Encode ports packages\/demo\/src\/codec\.ts:17 \(encode\)\.\n\/\/\n\/\/ Encodes one message\.\nfunc Encode\(message Message, options \*Options\) \(\[\]byte, error\) \{\n\tpanic\("unported: packages\/demo\/src\/codec\.ts#encode"\)/);
  assert.match(stub, /func NewCodec\(options Options\) \*Codec/);
  // A sealed union's variant drops its literal discriminant; the Go type is the discriminant.
  assert.match(stub, /type Hello struct \{\n\tVersion float64 `json:"version"`\n\}/);
  assert.match(stub, /type Frame interface \{\n\tisFrame\(\)\n\}/);
  assert.match(stub, /func \(Hello\) isFrame\(\) \{\}/);
  // An omitted all-optional options object is the struct's zero value.
  assert.match(stub, /func Limit\(limits Limits\) float64/);
  assert.match(stub, /func \(c \*Codec\) State\(\) float64/);
  assert.match(stub, /func \(c \*Codec\) Flush\(ctx context\.Context\) \(float64, error\)/);
  assert.match(stub, /func \(c \*Codec\) Push\(chunk \[\]byte\) \(\[\]Message, error\)/);
  const skeleton = fs.readFileSync(path.join(context.goDir, "codec_upstream_test.go"), "utf8");
  assert.match(skeleton, /func TestCodecUpstream\(t \*testing\.T\)/);
  assert.match(skeleton, /t\.Run\("codec › encodes", func\(t \*testing\.T\) \{\n\t\t\/\/ upstream: packages\/demo\/test\/codec\.test\.ts:3\n\t\tt\.Skip\("unported"\)/);
  assert.match(skeleton, /t\.Run\("standalone"/);
  goVet(context);

  const second = run(context, "--check");
  assert.equal(second.status, 0, second.stderr);
  assert.equal(second.stderr, "");
});

test("gen-go-stubs omits hand-written declarations and refuses to overwrite edited files", (t) => {
  const context = fixture(t);
  write(context.goDir, "codec.go", "package demo\n\n// Encode is hand-written.\nfunc Encode(message Message, options *Options) ([]byte, error) { return nil, nil }\n\ntype Codec struct{ n int }\n\nfunc (c *Codec) Push(chunk []byte) ([]Message, error) { return nil, nil }\n");
  assert.equal(run(context).status, 0);
  const stub = fs.readFileSync(path.join(context.goDir, "codec_stub.go"), "utf8");
  assert.doesNotMatch(stub, /func Encode\(/);
  assert.doesNotMatch(stub, /type Codec struct/);
  assert.doesNotMatch(stub, /\) Push\(/);
  assert.match(stub, /func \(c \*Codec\) Flush\(/);
  goVet(context);

  fs.appendFileSync(path.join(context.goDir, "codec_stub.go"), "\n// edited\n");
  const edited = run(context);
  assert.equal(edited.status, 2);
  assert.match(edited.stderr, /codec_stub\.go: generated file was edited/);

  fs.rmSync(path.join(context.goDir, "codec_stub.go"));
  write(context.goDir, "codec_stub.go", "package demo\n");
  const handWritten = run(context);
  assert.equal(handWritten.status, 2);
  assert.match(handWritten.stderr, /codec_stub\.go: hand-written file; the generator never overwrites it/);
});

test("ported cases leave the skeleton and flip the test-mapping row", (t) => {
  const context = fixture(t);
  const ledger = path.join(context.root, "test-mapping.json");
  write(context.root, "test-mapping.json", `${JSON.stringify({ upstreamVersion: "x", entries: [{ path: "packages/demo/test/codec.test.ts", disposition: "designed-out", upstreamTestHash: "sha256:old", rationale: "out of scope" }] }, null, 2)}\n`);
  const readEntry = () => JSON.parse(fs.readFileSync(ledger, "utf8")).entries[0];
  assert.equal(run(context, "--test-mapping", ledger).status, 0);
  // A reviewed designed-out row keeps its reason until every case is cited.
  let entry = readEntry();
  assert.equal(entry.disposition, "designed-out");
  assert.equal(entry.rationale, "out of scope");
  write(context.root, "test-mapping.json", `${JSON.stringify({ upstreamVersion: "x", entries: [{ path: "packages/demo/test/codec.test.ts", disposition: "pending", upstreamTestHash: "sha256:old" }] }, null, 2)}\n`);
  assert.equal(run(context, "--test-mapping", ledger).status, 0);
  entry = readEntry();
  assert.equal(entry.disposition, "pending");
  assert.notEqual(entry.upstreamTestHash, "sha256:old");

  write(context.goDir, "codec_test.go", [
    "package demo",
    "",
    'import "testing"',
    "",
    "func TestCodec(t *testing.T) {",
    "\t// upstream: packages/demo/test/codec.test.ts:3",
    "\t// upstream: packages/demo/test/codec.test.ts:4",
    "}",
    "",
  ].join("\n"));
  assert.equal(run(context, "--test-mapping", ledger).status, 0);
  entry = JSON.parse(fs.readFileSync(ledger, "utf8")).entries[0];
  assert.equal(entry.disposition, "partial");
  assert.match(entry.rationale, /6 standalone/);
  const skeleton = fs.readFileSync(path.join(context.goDir, "codec_upstream_test.go"), "utf8");
  assert.doesNotMatch(skeleton, /codec › encodes/);
  assert.match(skeleton, /"standalone"/);

  fs.appendFileSync(path.join(context.goDir, "codec_test.go"), "func TestStandalone(t *testing.T) {\n\t// upstream: packages/demo/test/codec.test.ts:6\n}\n");
  assert.equal(run(context, "--test-mapping", ledger).status, 0);
  entry = JSON.parse(fs.readFileSync(ledger, "utf8")).entries[0];
  assert.equal(entry.disposition, "ported");
  assert.deepEqual(entry.evidence, ["demo/codec_test.go#TestCodec", "demo/codec_test.go#TestStandalone"]);
  assert.match(entry.upstreamTestHash, /^sha256:[0-9a-f]{64}$/);
  assert.equal(fs.existsSync(path.join(context.goDir, "codec_upstream_test.go")), false);
  goVet(context);

  // Citing every case closes a designed-out row too.
  write(context.root, "test-mapping.json", `${JSON.stringify({ upstreamVersion: "x", entries: [{ path: "packages/demo/test/codec.test.ts", disposition: "designed-out", upstreamTestHash: "sha256:old", rationale: "out of scope" }] }, null, 2)}\n`);
  assert.equal(run(context, "--test-mapping", ledger).status, 0);
  assert.equal(readEntry().disposition, "ported");
});

test("a skeleton subtest still skipped as unported does not count as ported", (t) => {
  const context = fixture(t);
  write(context.goDir, "codec_upstream_test.go", [
    "package demo",
    "",
    'import "testing"',
    "",
    "func TestCodecUpstream(t *testing.T) {",
    '\tt.Run("encodes", func(t *testing.T) {',
    "\t\t// upstream: packages/demo/test/codec.test.ts:3",
    "\t})",
    '\tt.Run("rejects", func(t *testing.T) {',
    "\t\t// upstream: packages/demo/test/codec.test.ts:4",
    '\t\tt.Skip("unported")',
    "\t})",
    "}",
    "",
  ].join("\n"));
  const result = generateGoStubs({ sourceRoot: context.upstream, packageKey: "demo", outDir: context.goDir, goPackage: "demo", repoRoot: path.join(context.root, "go") });
  const [row] = result.rows;
  assert.equal(row.disposition, "partial");
  assert.deepEqual(row.missing, ["4 codec › rejects negative limits", "6 standalone"]);
  // The porter-claimed skeleton is never rewritten and is not a conflict.
  const written = writeOutputs(context.goDir, result.outputs, { check: true });
  assert.deepEqual(written.conflicts, []);
  assert.ok(!written.changes.some((change) => change.target.endsWith("codec_upstream_test.go")));
});

test("a hand-written struct field ports a class property, so no clashing method is stubbed", (t) => {
  const context = fixture(t);
  write(context.goDir, "codec.go", "package demo\n\n// Codec is hand-written; its State field ports the upstream getter.\ntype Codec struct {\n\tState float64\n\tother int\n}\n");
  const result = run(context, "--report", "-");
  assert.equal(result.status, 0, result.stderr);
  const stub = fs.readFileSync(path.join(context.goDir, "codec_stub.go"), "utf8");
  assert.doesNotMatch(stub, /\) State\(/);
  assert.match(stub, /func \(c \*Codec\) Flush\(/);
  const report = JSON.parse(result.stdout);
  assert.ok(report.present.some((item) => item.go === "Codec.State"));
  goVet(context);
});

// subpathFixture adds a ./testing subpath export whose API refers to the root entry's types.
function subpathFixture(t) {
  const context = fixture(t);
  write(context.upstream, "packages/demo/package.json", JSON.stringify({ name: "@x/demo", exports: { ".": { types: "./dist/index.d.ts" }, "./testing": { types: "./dist/testing/index.d.ts" } } }));
  write(context.upstream, "packages/demo/src/testing/index.ts", 'export { FakeSink, createFakeCodec } from "./fake.ts";\n');
  write(context.upstream, "packages/demo/src/testing/fake.ts", [
    'import { Codec, type Options, type Sink } from "../codec.ts";',
    "export class FakeSink implements Sink {",
    "  writes = 0;",
    "  async write(chunk: Uint8Array): Promise<void> { this.writes += chunk.length; }",
    "  close(): void {}",
    "}",
    "export function createFakeCodec(options: Options): Codec { return new Codec(options); }",
    "",
  ].join("\n"));
  return { ...context, testingDir: path.join(context.goDir, "demotest") };
}

test("a subpath delegated by a stubgen:subpath marker ports into its own Go package", (t) => {
  const context = subpathFixture(t);
  const before = run(context, "--report", "-");
  assert.equal(before.status, 0, before.stderr);
  assert.ok(JSON.parse(before.stdout).generated.some((item) => item.go === "FakeSink"));
  assert.ok(fs.existsSync(path.join(context.goDir, "testing_fake_stub.go")));

  // The root package delegates ./testing; its run drops that subpath and the stale stub file.
  write(context.goDir, "doc.go", "// Package demo is hand-written.\n//\n// stubgen:subpath ./testing\npackage demo\n");
  const root = run(context, "--report", "-");
  assert.equal(root.status, 0, root.stderr);
  assert.ok(!JSON.parse(root.stdout).generated.some((item) => item.upstream.includes("/testing/")));
  assert.equal(fs.existsSync(path.join(context.goDir, "testing_fake_stub.go")), false);
  assert.ok(fs.existsSync(path.join(context.goDir, "codec_upstream_test.go")));

  // The subpath run stubs only the subpath, refers to root types through the self import, and writes no test skeletons.
  const sub = spawnSync(process.execPath, [cli, "--source-root", context.upstream, "--package", "demo", "--subpath", "./testing", "--import", "@x/demo=fixture/demo", "--out", context.testingDir, "--go-package", "demotest", "--repo-root", path.join(context.root, "go"), "--report", "-"], { encoding: "utf8" });
  assert.equal(sub.status, 0, sub.stderr);
  const report = JSON.parse(sub.stdout);
  assert.deepEqual(report.testRows, []);
  assert.ok(report.generated.every((item) => item.upstream.includes("/testing/")));
  const stub = fs.readFileSync(path.join(context.testingDir, "testing_fake_stub.go"), "utf8");
  assert.match(stub, /^package demotest$/m);
  assert.match(stub, /"fixture\/demo"/);
  assert.match(stub, /func CreateFakeCodec\(options demo\.Options\) \*demo\.Codec/);
  assert.deepEqual(fs.readdirSync(context.testingDir).filter((name) => name.endsWith("_test.go")), []);
  goVet(context);
});

test("an unknown subpath is an error", (t) => {
  const context = subpathFixture(t);
  const result = run(context, "--subpath", "./missing");
  assert.equal(result.status, 1);
  assert.match(result.stderr, /packages\/demo has no public subpath \.\/missing/);
});
