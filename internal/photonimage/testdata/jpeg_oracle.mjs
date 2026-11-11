// Regenerates photon-jpeg-corpus.tsv: the JPEG bytes Photon's `PhotonImage.get_bytes_jpeg(quality)` writes for the corpus of
// jpeg_oracle_test.go, as length and SHA-256 (and the SHA-256 of each input's RGBA pixels, so a drifting input generator is told
// apart from a drifting encoder).
//
//   node jpeg_oracle.mjs <directory of an installed Photon package> > photon-jpeg-corpus.tsv
//
// The package is @silvia-odwyer/photon 0.3.3 (what packages/durable pins; web target, initSync) or @silvia-odwyer/photon-node
// 0.3.4 (what packages/coding-agent loads; CommonJS): both embed image 0.24.9, and both write the same bytes. The generators are in
// corpus.mjs, integer-only on purpose: jpeg_oracle_test.go has the same ones in Go.
//
// Columns: `image <name> <width> <height> <sha256 of rgba>` and `jpeg <name> <quality> <length> <sha256>`; a quality at which Photon
// panics (an AC coefficient the standard Huffman table has no code for) is `jpeg <name> <quality> error -`.
import { flats, loadPhoton, makePixels, sha256 } from "./corpus.mjs";

const { PhotonImage } = await loadPhoton(process.argv[2]);

const cases = [];
const sizes = [[1, 1], [2, 1], [1, 2], [7, 5], [8, 8], [9, 9], [15, 17], [16, 16], [17, 33], [64, 1], [1, 64], [100, 75], [257, 129]];
const everyQuality = Array.from({ length: 100 }, (_, i) => i + 1);
const someQualities = [1, 40, 55, 70, 80, 85, 100];
let seed = 1;
for (const kind of ["photo", "screenshot", "noise", "noisea", "hgrad", "diag"]) {
	for (const [w, h] of sizes) {
		const sweep = (kind === "photo" && w === 100) || (kind === "noise" && w === 17);
		cases.push({ name: `${kind}-${w}x${h}`, kind, w, h, seed: seed++, qualities: sweep ? everyQuality : someQualities });
	}
}
for (const [w, h] of [[8, 8], [9, 8], [33, 17]]) cases.push({ name: `checker-${w}x${h}`, kind: "checker", w, h, seed: seed++, qualities: someQualities });
for (const name of Object.keys(flats)) cases.push({ name: `flat-${name}`, kind: "flat", flat: flats[name], w: 8, h: 8, seed: seed++, qualities: someQualities });
for (const name of ["white", "black"]) cases.push({ name: `flat-${name}-odd`, kind: "flat", flat: flats[name], w: 3, h: 5, seed: seed++, qualities: someQualities });
for (const [kind, w, h, qualities] of [["photo", 1024, 768, [40, 80, 85]], ["screenshot", 1920, 1080, [40, 80, 85]], ["noise", 640, 480, [40, 80, 85]], ["photo", 4000, 3000, [80, 85]], ["screenshot", 2559, 1439, [85]]]) {
	cases.push({ name: `${kind}-${w}x${h}`, kind, w, h, seed: seed++, qualities });
}

for (const c of cases) {
	const pixels = makePixels(c.kind, c.flat, c.w, c.h, c.seed);
	console.log(`image\t${c.name}\t${c.w}\t${c.h}\t${sha256(pixels)}`);
	for (const quality of c.qualities) {
		const image = new PhotonImage(pixels, c.w, c.h);
		try {
			const bytes = image.get_bytes_jpeg(quality);
			console.log(`jpeg\t${c.name}\t${quality}\t${bytes.length}\t${sha256(bytes)}`);
		} catch {
			console.log(`jpeg\t${c.name}\t${quality}\terror\t-`);
		}
		image.free();
	}
}
