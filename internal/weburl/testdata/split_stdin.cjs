// Preloaded with `node --require` before web_url.mjs: replaces process.stdin with a stream that delivers the bytes piped to it one per chunk, so every
// multi-byte character is split across chunks the way a pipe read may split it, whatever the timing of the pipe.
const { readFileSync } = require("node:fs");
const { Readable } = require("node:stream");

const bytes = readFileSync(0);
const chunks = Array.from(bytes, (byte) => Buffer.of(byte));
Object.defineProperty(process, "stdin", { value: Readable.from(chunks, { objectMode: false }), configurable: true });
