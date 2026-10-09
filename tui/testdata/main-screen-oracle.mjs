// Generates main-screen-oracle.json: seeded random component trees, terminal sizes and frame sequences run through Pi's TuiMainScreen.
// Usage: node main-screen-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> > main-screen-oracle.json
// A scenario is {cols, rows, parts, steps}. A step may set the lines of one or more components, resize the terminal, toggle
// clear-on-shrink or the hardware cursor, force a full render, then renders once. The log holds the terminal bytes each step wrote
// (cursor visibility and movement calls are spelled as ProcessTerminal writes them) and the lines each component returned.
import { pathToFileURL } from "node:url";
const tui = await import(pathToFileURL(process.argv[2]).href);
const { TuiMainScreen, truncateToWidth, CURSOR_MARKER, Container, setCapabilities, resetCapabilitiesCache } = tui;
const { isImageLine } = await import(new URL("./terminal-image.js", pathToFileURL(process.argv[2])).href);

let seed = 0x7a11e5c1;
const random = () => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed / 0x100000000;
};
const pick = (list) => list[Math.floor(random() * list.length)];
const atoms = ["x".repeat(150), "y".repeat(40), "a", "bb", " ", "hello world", "日本語", "😀", "e\u0301", "\x1b[31m", "\x1b[0m", "\x1b[1m", "\x1b[38;5;99m", "\x1b[7m \x1b[0m", "\x1b]8;;http://x\x07", "\x1b]8;;\x07", "│", "─────", "ก็", "tab", "x".repeat(30)];
const lineText = () => Array.from({ length: Math.floor(random() * 6) }, () => pick(atoms)).join("");
const imageLines = {
	kitty: () => `\x1b_Ga=T,f=100,c=20,r=3,i=${1 + Math.floor(random() * 3)},C=1;AAAA\x1b\\`,
	iterm2: () => `\x1b]1337;File=inline=1;width=10:AAAA${Math.floor(random() * 3)}\x07`,
};
let imageKind = null;
const makeLines = (count, withMarker) => {
	const lines = Array.from({ length: count }, () => (imageKind && random() < 0.15 ? imageLines[imageKind]() : lineText()));
	if (withMarker && count > 0) {
		const i = Math.floor(random() * count);
		let at = Math.floor(random() * (lines[i].length + 1));
		while (at > 0 && at < lines[i].length && /[\udc00-\udfff]/.test(lines[i][at])) at--; // never split a surrogate pair (it cannot cross JSON)
		lines[i] = lines[i].slice(0, at) + CURSOR_MARKER + lines[i].slice(at);
	}
	return lines;
};
const sizes = [[20, 5], [40, 10], [80, 24], [12, 3], [60, 8], [100, 30], [33, 1], [80, 2]];
const scenarios = [];
for (let i = 0; i < 500; i++) {
	const [cols, rows] = pick(sizes);
	const images = pick([null, null, null, "kitty", "iterm2"]);
	imageKind = images;
	const parts = 1 + Math.floor(random() * 3);
	const nested = random() < 0.3;
	const raw = random() < 0.06; // the last frame's components return their lines unclipped, so a line wider than the terminal reaches doRender
	const current = Array.from({ length: parts }, () => []);
	const steps = [];
	const n = 3 + Math.floor(random() * 9);
	for (let s = 0; s < n; s++) {
		const step = { set: {} };
		const r = random();
		const touch = Math.floor(random() * parts);
		const mode = random();
		const countMax = pick([2, 5, 12, 40]);
		const count = mode < 0.1 ? 0 : Math.floor(random() * countMax);
		if (random() < 0.85 || s === 0) {
			const shape = random();
			// Append to or shrink the previous frame's lines, so the differential paths see unchanged prefixes.
			if (s > 0 && shape < 0.25 && current[touch].length > 0) step.set[touch] = current[touch].concat(makeLines(1 + Math.floor(random() * 4), random() < 0.4));
			else if (s > 0 && shape < 0.45 && current[touch].length > 0) step.set[touch] = current[touch].slice(0, Math.floor(random() * current[touch].length));
			else if (s > 0 && shape < 0.6 && current[touch].length > 0) {
				const edited = current[touch].slice();
				edited[Math.floor(random() * edited.length)] = lineText();
				step.set[touch] = edited;
			} else step.set[touch] = makeLines(count, random() < 0.4);
			current[touch] = step.set[touch];
		}
		if (random() < 0.3) step.set[Math.floor(random() * parts)] = makeLines(Math.floor(random() * countMax), false);
		if (r < 0.12) step.resize = pick(sizes);
		if (r > 0.9) step.force = true;
		if (random() < 0.08) step.clearOnShrink = random() < 0.5;
		if (random() < 0.08 && s > 0) { step.hardwareCursor = random() < 0.5; step.cursorFirst = random() < 0.5; } // before the first frame Pi paints one on request, PiG waits for Start
		if (random() < 0.25 && s > 0) {
			const sizeValue = () => pick([5, 10, 20, 40, "30%", "50%", "100%", undefined, undefined]);
			const spec = {
				width: sizeValue(), minWidth: pick([undefined, undefined, 8, 15]), maxHeight: pick([undefined, 2, 4, "50%"]),
				anchor: pick([undefined, "center", "top-left", "top-right", "bottom-left", "bottom-right", "top-center", "bottom-center", "left-center", "right-center"]),
				offsetX: pick([undefined, 0, 2, -3]), offsetY: pick([undefined, 0, 1, -1]),
				row: pick([undefined, undefined, 0, 3, "25%"]), col: pick([undefined, undefined, 0, 4, "50%"]),
				margin: pick([undefined, undefined, 1, { top: 1, left: 2 }, { bottom: 1, right: 3 }]),
				nonCapturing: pick([undefined, true, false]),
			};
			step.overlay = { spec, lines: makeLines(1 + Math.floor(random() * 6), false) };
		}
		if (random() < 0.12 && s > 0) step.hideOverlay = true;
		// The raw frame ends the scenario: it may throw on a line wider than the terminal (which only renderNow may do, a requested frame would throw from a timer), and Pig's D53 full clear of a formerly over-wide row makes a later frame differ on purpose.
		if (raw && s === n - 1) { step.raw = true; step.set[touch] = makeLines(1 + count, false).map((l) => l + "x".repeat(Math.floor(random() * 3) * 20)); delete step.overlay; delete step.hideOverlay; delete step.hardwareCursor; delete step.cursorFirst; }
		steps.push(step);
	}
	scenarios.push({ cols, rows, parts, nested, images, steps });
}
const mark = (s) => s;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
for (const sc of scenarios) {
	if (sc.images) setCapabilities({ images: sc.images, trueColor: true, hyperlinks: true }); else resetCapabilitiesCache();
	let out = "";
	const term = {
		columns: sc.cols,
		rows: sc.rows,
		kittyProtocolActive: false,
		start() {},
		stop() {},
		async drainInput() {},
		write(d) { out += d; },
		moveBy(n) { if (n > 0) out += `\x1b[${n}B`; else if (n < 0) out += `\x1b[${-n}A`; },
		hideCursor() { out += "\x1b[?25l"; },
		showCursor() { out += "\x1b[?25h"; },
		clearLine() { out += "\x1b[K"; },
		clearFromCursor() { out += "\x1b[J"; },
		clearScreen() { out += "\x1b[2J\x1b[H"; },
		setTitle() {},
		setProgress() {},
	};
	const ui = new TuiMainScreen(term);
	const specs = Array.from({ length: sc.parts }, () => ({ lines: [], rendered: [] }));
	const comps = specs.map((spec) => ({
		invalidate() {},
		render(width) { spec.rendered = curStep.raw ? spec.lines.slice() : spec.lines.map((l) => (isImageLine(l) ? l : truncateToWidth(l, width, ""))); return spec.rendered; },
	}));
	if (sc.nested && comps.length > 1) {
		const inner = new Container();
		for (const c of comps.slice(1)) inner.addChild(c);
		ui.addChild(comps[0]);
		ui.addChild(inner);
	} else for (const c of comps) ui.addChild(c);
	sc.log = [];
	const overlays = [];
	let curStep = null;
	for (const step of sc.steps) {
		curStep = step;
		out = "";
		for (const [k, lines] of Object.entries(step.set)) specs[Number(k)].lines = lines;
		if (step.resize) { term.columns = step.resize[0]; term.rows = step.resize[1]; }
		if (step.cursorFirst) ui.setShowHardwareCursor(step.hardwareCursor); // before the other changes: they must share one requested frame
		if (step.overlay) {
			const spec = { lines: step.overlay.lines, rendered: [] };
			const comp = { invalidate() {}, render(width) { spec.rendered = spec.lines.map((l) => (isImageLine(l) ? l : truncateToWidth(l, width, ""))); return spec.rendered; } };
			const options = Object.fromEntries(Object.entries(step.overlay.spec).filter(([, v]) => v !== undefined));
			overlays.push({ spec, handle: ui.showOverlay(comp, options) });
		}
		if (step.hideOverlay && overlays.length > 0) {
			const top = overlays.pop();
			top.handle.hide();
			top.hidden = true;
		}
		if (step.clearOnShrink !== undefined) ui.setClearOnShrink(step.clearOnShrink);
		if (step.hardwareCursor !== undefined && !step.cursorFirst) {
			ui.setShowHardwareCursor(step.hardwareCursor);
		}
		// Overlay and hardware-cursor changes request a frame, which Pi renders on the next tick after its 16 ms throttle; let it run before the step's own render (only when one is pending).
		if (ui.renderRequested || ui.renderTimer) await sleep(30);
		let error = null;
		try { ui.renderNow(!!step.force); } catch (e) { error = String(e && e.message).slice(0, 80); }
		sc.log.push({ out, rendered: specs.map((s) => s.rendered), overlays: overlays.map((o) => o.spec.rendered), error });
		if (error) break;
	}
}
process.stdout.write(JSON.stringify(scenarios) + "\n");
