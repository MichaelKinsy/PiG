// Regenerates photon-tiffdecode-corpus.tsv: what Photon's `PhotonImage.new_from_byteslice(bytes)` makes of each file of a directory, as the
// dimensions and the SHA-256 of `get_raw_pixels()` (RGBA8), or `error` where it throws. The files are the corpus of tiffdecode_oracle_test.go,
// which writes them to a directory when PHOTON_TIFF_CORPUS_DIR is set:
//
//   PHOTON_TIFF_CORPUS_DIR=/tmp/corpus go test -run TestDecodeTIFFMatchesPhoton ./internal/photonimage
//   node tiffdecode_oracle.mjs /tmp/corpus <photon directory>... > photon-tiffdecode-corpus.tsv
//
// Each photon directory holds an installed Photon package: @silvia-odwyer/photon 0.3.3 (what packages/durable pins; web target, initSync) and
// @silvia-odwyer/photon-node 0.3.4 (what packages/coding-agent loads; CommonJS). Both embed image 0.24.9 over tiff 0.9.1; with more than one
// directory the script fails when they disagree on a file. A Rust panic leaves the WebAssembly instance in an unknown state, so the package is
// loaded again after each throw.
//
// Columns: `tiff <name> <input length> <input sha256> <width> <height> <sha256 of the pixels>`; `error` replaces the last three after a throw.
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

let generation = 0;
// loadPhoton gives a fresh instance of the package in directory: the web build (0.3.3) is imported under a new URL each time, the CommonJS
// build (0.3.4, which has photon_rs_bg.js) is required again after its cache entries are dropped. Both start a new WebAssembly instance.
async function loadPhoton(directory) {
	directory = `${resolve(directory)}/`;
	generation++;
	let photon;
	if (existsSync(`${directory}photon_rs_bg.js`)) {
		const require = createRequire(directory);
		const path = require.resolve("./photon_rs.js");
		delete require.cache[path];
		delete require.cache[path.replace("photon_rs.js", "photon_rs_bg.js")];
		photon = require("./photon_rs.js");
	} else {
		photon = await import(`${pathToFileURL(`${directory}photon_rs.js`).href}?generation=${generation}`);
		if (!photon.PhotonImage) photon = photon.default;
		photon.initSync({ module: readFileSync(`${directory}photon_rs_bg.wasm`) });
	}
	return photon;
}

const corpus = process.argv[2];
const directories = process.argv.slice(3);
const photons = [];
for (const directory of directories) photons.push(await loadPhoton(directory));
let decoded = 0;

function decode(index, bytes) {
	try {
		const image = photons[index].PhotonImage.new_from_byteslice(new Uint8Array(bytes));
		const width = image.get_width();
		const height = image.get_height();
		const pixels = image.get_raw_pixels();
		const result = `${width}\t${height}\t${sha256(pixels)}`;
		image.free();
		return { result, big: width * height > 4_000_000 };
	} catch {
		return { result: "error\t-\t-", big: true };
	}
}

for (const name of readdirSync(corpus).filter((n) => n.endsWith(".tif")).sort()) {
	const bytes = readFileSync(`${corpus}/${name}`);
	const stem = name.slice(0, -".tif".length);
	let line;
	let reload = false;
	for (let i = 0; i < photons.length; i++) {
		const { result, big } = decode(i, bytes);
		reload ||= big;
		if (i === 0) line = result;
		else if (result !== line) {
			console.error(`${stem}: ${directories[0]} gives ${line.replaceAll("\t", " ")}, ${directories[i]} gives ${result.replaceAll("\t", " ")}`);
			process.exitCode = 1;
		}
		if (big) photons[i] = await loadPhoton(directories[i]);
	}
	// The linear memory of an instance only grows: start again before it gets to gigabytes.
	if (++decoded % 64 === 0 && !reload) for (let i = 0; i < photons.length; i++) photons[i] = await loadPhoton(directories[i]);
	console.log(`tiff\t${stem}\t${bytes.length}\t${sha256(bytes)}\t${line}`);
}
