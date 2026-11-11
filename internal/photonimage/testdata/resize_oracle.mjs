// Regenerates photon-resize-corpus.tsv: the pixels Photon's `resize(image, width, height, SamplingFilter.Lanczos3)` writes for the
// corpus of lanczos_oracle_test.go's TestResizeLanczos3MatchesPhotonOnAWideImage, as the SHA-256 of the output's RGBA bytes (and of
// each input's, so a drifting input generator is told apart from a drifting resize).
//
//   node resize_oracle.mjs <directory of an installed Photon package> > photon-resize-corpus.tsv
//
// The package is @silvia-odwyer/photon 0.3.3 or @silvia-odwyer/photon-node 0.3.4 (see jpeg_oracle.mjs); the generators are in corpus.mjs.
//
// Columns: `resize <kind> <seed> <width> <height> <new width> <new height> <sha256 of the input rgba> <sha256 of the output rgba>`.
// The last pictures are the ones on which `sin` matters (see `sensitive`). The sizes are wide and tall strips and ratios near 1 as well as small ones: the kernel's weights come from f32 `sin`, which a wide
// window and a ratio near 1 exercise on many different arguments.
import { loadPhoton, makePixels, rng, sha256 } from "./corpus.mjs";

const photon = await loadPhoton(process.argv[2]);
const { PhotonImage, resize, SamplingFilter } = photon;

// Pictures on which f32 `sin` from libm and the correctly rounded sine give weights that round one channel of a pixel differently
// (found by comparing the two kernels' outputs over random strips): [kind, seed, width, height, new width, new height].
const sensitive = [
	["diag", 1227, 2962, 49, 1481, 46], ["diag", 1475, 1832, 59, 916, 54], ["diag", 1503, 2240, 62, 2212, 57], ["diag", 1527, 1814, 25, 907, 24],
	["diag", 1535, 2830, 49, 1417, 46], ["diag", 2455, 1890, 59, 1814, 56], ["diag", 2835, 2232, 10, 2154, 9], ["diag", 3487, 2386, 25, 1193, 24],
	["screenshot", 4224, 2387, 48, 2386, 47], ["diag", 4295, 2064, 49, 2022, 46], ["screenshot", 4964, 1920, 59, 1830, 56],
];

const kinds = ["photo", "noise", "screenshot", "diag", "noisea", "hgrad"];
const sizes = rng(20261031);
const below = (n) => sizes() % n;
for (let seed = 1; seed <= 240; seed++) {
	const kind = kinds[seed % kinds.length];
	let w, h;
	switch (seed % 4) {
		case 0: // a wide strip
			w = 1000 + below(2000);
			h = 1 + below(80);
			break;
		case 1: // a tall strip
			w = 1 + below(80);
			h = 1000 + below(2000);
			break;
		default:
			w = 100 + below(700);
			h = 100 + below(700);
	}
	let toW, toH;
	switch (below(4)) {
		case 0: // just smaller
			toW = Math.max(1, w - 1 - below(Math.max(1, w >> 6)));
			toH = Math.max(1, h - 1 - below(Math.max(1, h >> 6)));
			break;
		case 1: // about half
			toW = Math.max(1, (w >> 1) + below(3));
			toH = Math.max(1, (h >> 1) + below(3));
			break;
		case 2: // larger
			toW = w + 1 + below(w);
			toH = h + 1 + below(h);
			break;
		default:
			toW = 1 + below(w);
			toH = 1 + below(h);
	}
	const pixels = makePixels(kind, undefined, w, h, seed);
	const image = new PhotonImage(pixels, w, h);
	const resized = resize(image, toW, toH, SamplingFilter.Lanczos3);
	console.log(["resize", kind, seed, w, h, toW, toH, sha256(pixels), sha256(resized.get_raw_pixels())].join("\t"));
	resized.free();
	image.free();
}

for (const [kind, seed, w, h, toW, toH] of sensitive) {
	const pixels = makePixels(kind, undefined, w, h, seed);
	const image = new PhotonImage(pixels, w, h);
	const resized = resize(image, toW, toH, SamplingFilter.Lanczos3);
	console.log(["resize", kind, seed, w, h, toW, toH, sha256(pixels), sha256(resized.get_raw_pixels())].join("\t"));
	resized.free();
	image.free();
}
