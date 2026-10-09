// Seeded PRNG (mulberry32) shared by the oracle generators.
export function rng(seed) {
	let a = seed >>> 0;
	const next = () => {
		a = (a + 0x6d2b79f5) >>> 0;
		let t = a;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
	next.int = (n) => Math.floor(next() * n);
	next.pick = (xs) => xs[next.int(xs.length)];
	next.chance = (p) => next() < p;
	return next;
}

export const hex = (s) => (s === "" ? "." : Buffer.from(s, "utf8").toString("hex"));
