// Generates output-golden.json: Pi's packages/durable/src/codemode/output.ts formatResult run on a corpus. Run from the
// repository root with Node >= 24 (native type stripping):
//   PI_UPSTREAM=<pi tree> node durable/codemode/testdata/output-oracle.mjs > durable/codemode/testdata/output-golden.json
// The upstream mirror is .upstream/current unless PI_UPSTREAM names another. The fake writer names its files /out/<name>.
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = resolve(process.env.PI_UPSTREAM ?? ".upstream/current", "packages/durable/src/codemode");
const { formatResult } = await import(pathToFileURL(resolve(root, "output.ts")).href);

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";
const LIMITS = { maxBytes: 4 * 1024 * 1024, maxLines: 100_000 };
const text = (value, console_) => ({ type: "text", text: value, ...(console_ ? { console: true } : {}) });
const image = (data, mimeType = "image/png") => ({ type: "image", data, mimeType });
const ok = (output, value) => ({ ok: true, output, ...(value === undefined ? {} : { value }), calls: [], storeWrites: { set: {}, delete: [] } });
const fail = (output, error) => ({ ok: false, output, error, calls: [] });

const cases = [
	{ name: "value only", result: ok([], "done"), wallMs: 1234 },
	{ name: "object value", result: ok([], { a: [1, "x"], b: null }), wallMs: 250 },
	{ name: "text items and console", result: ok([text("one"), text("log a", true), text("two"), text("log b", true)], 7), wallMs: 99 },
	{ name: "single text and console", result: ok([text("one"), text("log", true)]), wallMs: 0 },
	{ name: "images saved once", result: ok([image(PNG), text("between"), image(PNG), image(PNG, "image/jpeg")]), wallMs: 15050 },
	{ name: "unknown image type", result: ok([image(PNG, "image/bmp")]), wallMs: 1 },
	{ name: "script error", result: fail([text("partial")], { kind: "script", name: "TypeError", message: "x is not a function", stack: "TypeError: x is not a function\n    at <eval>:1" }), wallMs: 500, calls: [{ name: "echo", status: "ok" }, { name: "fails", status: "error" }] },
	{ name: "script error without stack", result: fail([], { kind: "script", message: "boom" }), wallMs: 500 },
	{ name: "named script error without stack", result: fail([], { kind: "script", name: "RangeError", message: "bad" }), wallMs: 500 },
	{ name: "timeout", result: fail([], { kind: "timeout", message: "after 300 ms" }), wallMs: 300000 },
	{ name: "aborted", result: fail([], { kind: "aborted", message: "stopped" }), wallMs: 12 },
	{ name: "sandbox", result: fail([], { kind: "sandbox", message: "crashed" }), wallMs: 12 },
	{ name: "generated images not shown", result: ok([text("x")]), wallMs: 1, generatedImages: 2 },
	{ name: "one generated image not shown", result: ok([], "v"), wallMs: 1, generatedImages: 1 },
	{ name: "generated images shown", result: ok([image(PNG)]), wallMs: 1, generatedImages: 3 },
	{ name: "truncated", result: ok([text("0123456789".repeat(30))]), wallMs: 1, maxOutputTokens: 10 },
	{ name: "truncated lines", result: ok([text(Array.from({ length: 50 }, (_, i) => `line ${i}`).join("\n"))]), wallMs: 1, maxOutputTokens: 20 },
	{ name: "truncated with images and console", result: ok([text("a".repeat(100)), image(PNG), text("c".repeat(100), true)]), wallMs: 1, maxOutputTokens: 10 },
	{ name: "truncated utf16", result: ok([text("😀é".repeat(40))]), wallMs: 1, maxOutputTokens: 10 },
	{ name: "truncated write fails", result: ok([text("z".repeat(200))]), wallMs: 1, maxOutputTokens: 10, writeFails: true },
	{ name: "image write fails", result: ok([image(PNG)]), wallMs: 1, writeFails: true },
	{ name: "limit lines", result: ok([text(Array.from({ length: 30 }, (_, i) => `row ${i}`).join("\n"))]), wallMs: 1, limits: { maxBytes: 1000, maxLines: 110 } },
	{ name: "limit lines tiny", result: ok([text(Array.from({ length: 30 }, (_, i) => `row ${i}`).join("\n"))]), wallMs: 1, limits: { maxBytes: 100000, maxLines: 100 } },
	{ name: "byte limit below the token budget", result: ok([text("q".repeat(300))]), wallMs: 1, limits: { maxBytes: 400, maxLines: 100_000 } },
	{ name: "trailing newline then text", result: ok([text("a\n"), text("b")]), wallMs: 1 },
	{ name: "within budget", result: ok([text("short")]), wallMs: 1, maxOutputTokens: 10 },
];

const out = [];
for (const c of cases) {
	const written = [];
	const write = async (name, data) => {
		written.push({ name, size: typeof data === "string" ? data.length : data.length, text: typeof data === "string" ? data : undefined });
		return c.writeFails ? { error: "disk full" } : { path: `/out/${name}` };
	};
	const result = { ...c.result };
	let formatted;
	try {
		formatted = await formatResult(result, { generatedImages: c.generatedImages ?? 0, wallMs: c.wallMs, maxOutputTokens: c.maxOutputTokens ?? 10_000, calls: c.calls ?? [], limits: c.limits ?? LIMITS, write });
	} catch (error) {
		formatted = { error: String(error) };
	}
	out.push({ name: c.name, input: { result: c.result, wallMs: c.wallMs, generatedImages: c.generatedImages ?? 0, maxOutputTokens: c.maxOutputTokens ?? 10_000, calls: c.calls ?? [], limits: c.limits ?? LIMITS, writeFails: c.writeFails ?? false }, formatted, written });
}
process.stdout.write(`${JSON.stringify(out, null, 1)}\n`);
