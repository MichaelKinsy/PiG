// Regenerates photon-webpdecode-corpus.tsv: what Photon's `PhotonImage.new_from_byteslice(bytes)` makes of each file of a directory, as the
// dimensions and the SHA-256 of `get_raw_pixels()` (RGBA8), or `error` where it throws. The files are the corpus of webpdecode_oracle_test.go,
// which writes them to a directory when PHOTON_WEBP_CORPUS_DIR is set:
//
//   PHOTON_WEBP_CORPUS_DIR=/tmp/corpus go test -run TestDecodeWebPMatchesPhoton ./internal/photonimage
//   node webpdecode_oracle.mjs <directory of an installed Photon package> /tmp/corpus > photon-webpdecode-corpus.tsv
//
// The package is @silvia-odwyer/photon 0.3.3 (what packages/durable pins; web target, initSync) or @silvia-odwyer/photon-node 0.3.4 (what
// packages/coding-agent loads; CommonJS): both embed image 0.24.9, and both give the same table. A Rust panic leaves the WebAssembly instance in
// an unknown state, so the package is loaded again after each throw.
//
// Columns: `webp <name> <input length> <input sha256> <width> <height> <sha256 of the pixels>`; `error` replaces the last three after a throw.
import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

let generation = 0;
async function loadPhoton(directory) {
	directory = directory.replace(/\/?$/, "/");
	generation++;
	let photon;
	try {
		photon = await import(`${pathToFileURL(`${directory}photon_rs.js`).href}?generation=${generation}`);
		if (!photon.PhotonImage) photon = photon.default;
	} catch {
		const require = createRequire(directory);
		const path = require.resolve("./photon_rs.js");
		delete require.cache[path];
		delete require.cache[path.replace("photon_rs.js", "photon_rs_bg.js")];
		photon = require("./photon_rs.js");
	}
	if (typeof photon.initSync === "function") photon.initSync({ module: readFileSync(`${directory}photon_rs_bg.wasm`) });
	return photon;
}

const directory = process.argv[2];
const corpus = process.argv[3];
let photon = await loadPhoton(directory);
let decoded = 0;

for (const name of readdirSync(corpus).filter((n) => n.endsWith(".webp")).sort()) {
	const bytes = readFileSync(`${corpus}/${name}`);
	const stem = name.slice(0, -".webp".length);
	let line;
	try {
		const image = photon.PhotonImage.new_from_byteslice(new Uint8Array(bytes));
		const width = image.get_width();
		const height = image.get_height();
		const pixels = image.get_raw_pixels();
		line = `webp\t${stem}\t${bytes.length}\t${sha256(bytes)}\t${width}\t${height}\t${sha256(pixels)}`;
		image.free();
		// The linear memory of an instance only grows: start again before it gets to gigabytes.
		if (++decoded % 64 === 0 || width * height > 4_000_000) photon = await loadPhoton(directory);
	} catch {
		line = `webp\t${stem}\t${bytes.length}\t${sha256(bytes)}\terror\t-\t-`;
		photon = await loadPhoton(directory);
	}
	console.log(line);
}
