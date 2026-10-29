// Regenerates photon-jpegdecode-corpus.tsv: what Photon's `PhotonImage.new_from_byteslice(bytes)` makes of the JPEG files of a directory
// (the corpus jpegdecode_corpus_test.go writes with `go test -run TestWriteJPEGDecodeCorpus` and PHOTON_JPEGDECODE_CORPUS_DIR=<dir>): the
// width, height and SHA-256 of `get_raw_pixels()`, or `error` where Photon throws (an unwrap on a decoding error, a panic).
//
//   node jpegdecode_oracle.mjs <directory of an installed Photon package> <directory of .jpg files> > photon-jpegdecode-corpus.tsv
//
// The package is @silvia-odwyer/photon 0.3.3 (what packages/durable pins; web target, initSync) or @silvia-odwyer/photon-node 0.3.4
// (what packages/coding-agent loads; CommonJS): both embed image 0.24.9 and jpeg-decoder 0.3.1, and both give the same pixels.
//
// Columns: `jpeg <name> <input length> <input sha256> <width> <height> <sha256 of the RGBA pixels>`; a failure is
// `jpeg <name> <input length> <input sha256> error - -`.
import { readdirSync, readFileSync } from "node:fs";
import { loadPhoton, sha256 } from "./corpus.mjs";

const photonDirectory = process.argv[2];
const corpusDirectory = process.argv[3].replace(/\/?$/, "/");
let { PhotonImage } = await loadPhoton(photonDirectory);

for (const file of readdirSync(corpusDirectory).filter((f) => f.endsWith(".jpg")).sort()) {
	const bytes = readFileSync(corpusDirectory + file);
	const name = file.slice(0, -4);
	let row;
	try {
		const image = PhotonImage.new_from_byteslice(new Uint8Array(bytes));
		const pixels = image.get_raw_pixels();
		row = `${image.get_width()}\t${image.get_height()}\t${sha256(pixels)}`;
		image.free();
	} catch {
		row = "error\t-\t-";
		// A panic leaves the module in an unknown state: start from a fresh instance (the web package can; the CommonJS one only
		// ever runs a corpus without errors being fatal to later rows, which the oracle checks by comparing both packages).
		({ PhotonImage } = await loadPhoton(photonDirectory));
	}
	console.log(`jpeg\t${name}\t${bytes.length}\t${sha256(bytes)}\t${row}`);
}
