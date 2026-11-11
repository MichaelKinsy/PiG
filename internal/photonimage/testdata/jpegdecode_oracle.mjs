// Regenerates photon-jpegdecode-corpus.tsv: what Photon's `PhotonImage.new_from_byteslice(bytes)` makes of the JPEG files of a directory
// (the corpus jpegdecode_corpus_test.go writes with `go test -run TestDecodeJPEGMatchesPhoton -jpegdecode.dump=<dir>`): the width, height
// and SHA-256 of `get_raw_pixels()`, or `error` where Photon throws (an unwrap on a decoding error, a panic).
//
//   node jpegdecode_oracle.mjs <directory of an installed Photon package> <directory of .jpg files> > photon-jpegdecode-corpus.tsv
//
// The package is @silvia-odwyer/photon 0.3.3 (what packages/durable pins; web target, initSync) or @silvia-odwyer/photon-node 0.3.4
// (what packages/coding-agent loads; CommonJS): both embed image 0.24.9 and jpeg-decoder 0.3.1, and both give the same pixels.
//
// A Rust panic is a WebAssembly trap, which leaves the instance with the shadow stack and the heap of the panicking call still taken
// (nothing unwinds), so after a few thousand panics an instance fails calls that would succeed. Every panic therefore ends the
// instance: the next file runs in a fresh one (a fresh copy of the glue code, and its own instantiation of the compiled module).
//
// Columns: `jpeg <name> <input length> <input sha256> <width> <height> <sha256 of the RGBA pixels>`; a failure is
// `jpeg <name> <input length> <input sha256> error - -`.
import { readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { sha256 } from "./corpus.mjs";

const photonDirectory = process.argv[2].replace(/\/?$/, "/");
const corpusDirectory = process.argv[3].replace(/\/?$/, "/");
const compiled = new WebAssembly.Module(readFileSync(`${photonDirectory}photon_rs_bg.wasm`));

let generation = 0;
// loadFresh loads the Photon package into a new instance.
async function loadFresh() {
	generation++;
	let photon;
	try {
		photon = await import(`${pathToFileURL(`${photonDirectory}photon_rs.js`).href}?instance=${generation}`);
		if (!photon.PhotonImage) photon = photon.default;
	} catch {
		const require = createRequire(photonDirectory);
		for (const file of ["./photon_rs.js", "./photon_rs_bg.js"]) {
			try {
				delete require.cache[require.resolve(file)];
			} catch {
				// the web package has no photon_rs_bg.js
			}
		}
		photon = require("./photon_rs.js");
	}
	if (typeof photon.initSync === "function") photon.initSync({ module: compiled });
	return photon;
}

let { PhotonImage } = await loadFresh();
let sinceFresh = 0;

for (const file of readdirSync(corpusDirectory).filter((f) => f.endsWith(".jpg")).sort()) {
	const bytes = readFileSync(corpusDirectory + file);
	const name = file.slice(0, -4);
	let row;
	let failed = false;
	try {
		const image = PhotonImage.new_from_byteslice(new Uint8Array(bytes));
		const pixels = image.get_raw_pixels();
		row = `${image.get_width()}\t${image.get_height()}\t${sha256(pixels)}`;
		image.free();
	} catch {
		row = "error\t-\t-";
		failed = true;
	}
	console.log(`jpeg\t${name}\t${bytes.length}\t${sha256(bytes)}\t${row}`);
	if (failed || ++sinceFresh >= 500) {
		({ PhotonImage } = await loadFresh());
		sinceFresh = 0;
	}
}
