// The integer-only picture generators shared by jpeg_oracle.mjs and resize_oracle.mjs; jpeg_oracle_test.go has the same ones in Go
// (corpusPixel and its helpers), so that a Go test can rebuild the pixels an oracle script gave Photon.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";

// loadPhoton loads the Photon package in directory: @silvia-odwyer/photon 0.3.3 (web target, initSync) or
// @silvia-odwyer/photon-node 0.3.4 (CommonJS); both embed image 0.24.9.
export async function loadPhoton(directory) {
	directory = directory.replace(/\/?$/, "/");
	let photon;
	try {
		photon = await import(pathToFileURL(`${directory}photon_rs.js`).href);
		if (!photon.PhotonImage) photon = photon.default;
	} catch {
		photon = createRequire(directory)("./photon_rs.js");
	}
	if (typeof photon.initSync === "function") photon.initSync({ module: readFileSync(`${directory}photon_rs_bg.wasm`) });
	return photon;
}

export const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

// xorshift32.
export function rng(seed) {
	let s = seed >>> 0 || 1;
	return () => {
		s = (s ^ (s << 13)) >>> 0;
		s = (s ^ (s >>> 17)) >>> 0;
		s = (s ^ (s << 5)) >>> 0;
		return s;
	};
}
function hash32(a) {
	a = Math.imul(a ^ (a >>> 16), 0x45d9f3b) >>> 0;
	a = Math.imul(a ^ (a >>> 16), 0x45d9f3b) >>> 0;
	return (a ^ (a >>> 16)) >>> 0;
}
const clamp = (v) => (v < 0 ? 0 : v > 255 ? 255 : v);
// lattice is a value-noise octave: random values on a size-pixel grid, bilinearly interpolated in integers.
function lattice(channel, octave, size, x, y) {
	const value = (i, j) => hash32((Math.imul(i, 73856093) ^ Math.imul(j, 19349663) ^ Math.imul(channel + 1, 83492791) ^ Math.imul(octave + 1, 668265263)) >>> 0) >>> 24;
	const i = Math.floor(x / size), j = Math.floor(y / size), fx = x % size, fy = y % size;
	const sum = value(i, j) * (size - fx) * (size - fy) + value(i + 1, j) * fx * (size - fy) + value(i, j + 1) * (size - fx) * fy + value(i + 1, j + 1) * fx * fy;
	return Math.floor(sum / (size * size));
}

export const generators = {
	noise: (x, y, w, h, rnd) => [rnd() & 255, (rnd() >>> 8) & 255, (rnd() >>> 16) & 255, 255],
	noisea: (x, y, w, h, rnd) => [rnd() & 255, (rnd() >>> 8) & 255, (rnd() >>> 16) & 255, rnd() >>> 24],
	hgrad: (x, y, w, h) => [Math.floor((x * 255) / Math.max(1, w - 1)), Math.floor((y * 255) / Math.max(1, h - 1)), Math.floor(((x + y) * 255) / Math.max(1, w + h - 2)), 255],
	diag: (x, y) => [(x * 3 + y * 5) & 255, (x * 7) & 255, (y * 11 + x) & 255, 255 - ((x + y) & 63)],
	checker: (x, y) => ((x + y) & 1 ? [255, 255, 255, 255] : [0, 0, 0, 255]),
	photo: (x, y, w, h, rnd) => {
		const pixel = [0, 0, 0, 255];
		for (let c = 0; c < 3; c++) {
			const v = (lattice(c, 0, 32, x, y) * 5 + lattice(c, 1, 8, x, y) * 3) >> 3;
			pixel[c] = clamp(v + (rnd() & 7) - 3);
		}
		return pixel;
	},
	screenshot: (x, y) => {
		if (y < 24) return [40 + (x >> 3 & 31), 60, 90 + (x >> 4 & 63), 255];
		if (x >= 20 && x < 120 && y >= 40 && y < 64) return [0, 102, 204, 255];
		if (x % 64 === 0 || y % 64 === 0) return [200, 200, 200, 255];
		const row = (y - 24) % 14;
		if (row >= 3 && row < 10) {
			const cell = hash32(Math.imul(Math.floor(x / 6) + 1, 2654435761) ^ Math.imul(Math.floor((y - 24) / 14) + 1, 40503)) >>> 0;
			if (cell % 3 !== 0 && x % 6 !== 5) {
				const edge = hash32(Math.imul(x + 1, 374761393) ^ Math.imul(y + 1, 668265263)) % 3;
				return [30 + edge * 60, 30 + edge * 60, 30 + edge * 60, 255];
			}
		}
		return [245, 245, 245, 255];
	},
};
export const flats = { white: [255, 255, 255, 255], black: [0, 0, 0, 255], red: [255, 0, 0, 255], green: [0, 255, 0, 255], blue: [0, 0, 255, 255], gray: [128, 128, 128, 255], clear: [10, 200, 30, 0], half: [255, 128, 0, 128] };


// makePixels is the RGBA pixels of the generator kind; flat is the colour of kind "flat".
export function makePixels(kind, flat, w, h, seed) {
	const rnd = rng(seed * 2654435761);
	const pixels = new Uint8Array(w * h * 4);
	for (let y = 0; y < h; y++) {
		for (let x = 0; x < w; x++) {
			pixels.set(kind === "flat" ? flat : generators[kind](x, y, w, h, rnd), (y * w + x) * 4);
		}
	}
	return pixels;
}
