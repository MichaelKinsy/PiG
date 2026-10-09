// Generates alt-screen-oracle.json: seeded random component trees, terminal sizes and frame sequences run through Pi's TuiAltScreen.
// Usage: node alt-screen-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> > alt-screen-oracle.json
// A scenario is {cols, rows, parts, steps}. A step may set the lines of one or more components, resize the terminal, toggle the
// hardware cursor, scroll the primary view, show or hide an overlay, force a full render, then renders once. The log holds the
// bytes start() wrote and the bytes each step wrote, and the lines each component returned.
import { pathToFileURL } from "node:url";
const tui = await import(pathToFileURL(process.argv[2]).href);
const { TuiAltScreen, Image, getCapabilities, truncateToWidth, CURSOR_MARKER, Container, setCapabilities, resetCapabilitiesCache, setCellDimensions } = tui;
const { isImageLine } = await import(new URL("./terminal-image.js", pathToFileURL(process.argv[2])).href);

for (const name of ["TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"]) delete process.env[name];
process.env.TERM = "xterm-256color";
let seed = 0x41757c33;
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
// A part may be a real Image component instead of lines; its transmission is what the screen's Kitty image cache tracks.
const imageSpec = () => ({
	data: pick(["QUJDRA==", "QUJDRA==", "QUJDRQ==", "A".repeat(5000) + "=="]),
	dims: pick([[100, 50], [50, 100], [640, 480], [3, 2000], [5000, 4000], [4096, 4096], [4096, 4097]]),
	imageId: pick([1, 2, 3, 4, 5]),
	maxWidthCells: pick([undefined, undefined, 10, 20]),
	maxHeightCells: pick([undefined, undefined, 3, 8]),
});
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
const sizes = [[20, 5], [40, 10], [80, 24], [12, 3], [60, 8], [100, 30], [33, 1], [80, 2], [1, 1], [0, 3]];
const scenarios = [];
for (let i = 0; i < 500; i++) {
	const [cols, rows] = pick(sizes);
	const images = pick([null, null, "kitty", "kitty", "iterm2"]);
	const churn = images === "kitty" && random() < 0.12; // many distinct images in a row, so the offscreen Kitty cache must evict
	const wez = random() < 0.3; // TERM_PROGRAM=WezTerm: Kitty images are drawn after every text write
	const tmux = random() < 0.2; // TMUX set: the mouse sequence is button-motion tracking
	imageKind = images;
	const parts = churn ? 1 : 1 + Math.floor(random() * 3);
	const nested = random() < 0.3;
	const current = Array.from({ length: parts }, () => []);
	const steps = [];
	const n = churn ? 24 + Math.floor(random() * 8) : 3 + Math.floor(random() * 9);
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
			} else if (images && random() < 0.4) step.set[touch] = { image: imageSpec() };
			else step.set[touch] = makeLines(count, random() < 0.4);
			current[touch] = Array.isArray(step.set[touch]) ? step.set[touch] : [];
		}
		if (churn) { const k = Math.floor(random() * parts); const spec = imageSpec(); spec.imageId = 1 + s; if (random() < 0.2) spec.dims = pick([[5000, 4000], [4096, 4096], [4096, 4097]]); else spec.dims = [100, 50]; spec.maxHeightCells = 2; step.set[k] = { image: spec }; current[k] = []; }
		if (random() < 0.3) { const k = Math.floor(random() * parts); step.set[k] = images && random() < 0.3 ? { image: imageSpec() } : makeLines(Math.floor(random() * countMax), false); current[k] = Array.isArray(step.set[k]) ? step.set[k] : []; }
		if (r < 0.12) step.resize = pick(sizes);
		if (r > 0.9) step.force = true;
		if (random() < 0.3 && s > 0) { const kind = random(); if (kind < 0.6) step.scrollBy = pick([-20, -5, -3, -1, 1, 2, 3, 7, 30]); else if (kind < 0.8) step.scrollTop = true; else step.scrollBottom = true; }
		if (random() < 0.1) step.flash = { message: pick(["saved", "日本語 done", "x".repeat(50), "", "a\nb"]), durationMs: 600000 };
		if (random() < 0.08) step.raw = true; // components return their lines unclipped: the screen cuts them at the terminal width
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
		steps.push(step);
	}
	scenarios.push({ cols, rows, parts, nested, images, wez, tmux, layoutRoot: random() < 0.2, preserve: random() < 0.3, steps });
}
const mark = (s) => s;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
for (const sc of scenarios) {
	if (sc.wez) process.env.TERM_PROGRAM = "WezTerm"; else delete process.env.TERM_PROGRAM;
	if (sc.tmux) process.env.TMUX = "1"; else delete process.env.TMUX;
	setCellDimensions({ widthPx: 9, heightPx: 18 });
	setCapabilities({ images: sc.images, trueColor: true, hyperlinks: true }); // explicit, so the environment flags above never change what the terminal is detected as
	let out = "";
	const term = {
		columns: sc.cols,
		rows: sc.rows,
		kittyProtocolActive: false,
		start() { out += "<terminal.start>"; },
		stop() { out += "<terminal.stop>"; },
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
	const ui = new TuiAltScreen(term);
	let curStep = { raw: false };
	const specs = Array.from({ length: sc.parts }, () => ({ lines: [], rendered: [], image: null }));
	const comps = specs.map((spec) => ({
		invalidate() {},
		render(width) { if (spec.image) { spec.rendered = null; return spec.image.render(width); } spec.rendered = curStep.raw ? spec.lines.slice() : spec.lines.map((l) => (isImageLine(l) ? l : truncateToWidth(l, width, ""))); return spec.rendered; },
	}));
	const host = sc.layoutRoot ? new Container() : ui; // a layout root replaces the screen's own children as the document
	if (sc.nested && comps.length > 1) {
		const inner = new Container();
		for (const c of comps.slice(1)) inner.addChild(c);
		host.addChild(comps[0]);
		host.addChild(inner);
	} else for (const c of comps) host.addChild(c);
	if (sc.layoutRoot) ui.setLayoutRoot(host);
	sc.log = [];
	out = "";
	ui.start();
	await sleep(30); // start() requests a frame, drawn on the next tick
	sc.startOut = out;
	sc.imagesAfterStart = getCapabilities().images ?? ""; // iTerm2 images are suppressed while the alternate screen is up
	const overlays = [];
	for (const step of sc.steps) {
		curStep = step;
		out = "";
		for (const [k, value] of Object.entries(step.set)) {
			const spec = specs[Number(k)];
			if (Array.isArray(value)) { spec.lines = value; spec.image = null; continue; }
			const options = { imageId: value.image.imageId };
			if (value.image.maxWidthCells !== undefined) options.maxWidthCells = value.image.maxWidthCells;
			if (value.image.maxHeightCells !== undefined) options.maxHeightCells = value.image.maxHeightCells;
			spec.image = new Image(value.image.data, "image/png", { fallbackColor: (text) => text }, options, { widthPx: value.image.dims[0], heightPx: value.image.dims[1] });
		}
		if (step.resize) { term.columns = step.resize[0]; term.rows = step.resize[1]; }
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
		const setCursor = () => { if (step.hardwareCursor !== undefined) ui.setShowHardwareCursor(step.hardwareCursor); };
		if (step.cursorFirst) setCursor();
		if (step.flash) ui.flash(step.flash.message, step.flash.durationMs);
		if (step.scrollBy !== undefined) ui.scrollBy(step.scrollBy);
		if (step.scrollTop) ui.scrollToTop();
		if (step.scrollBottom) ui.scrollToBottom();
		if (!step.cursorFirst) setCursor();
		// Overlay and hardware-cursor changes request a frame, which Pi renders on the next tick after its 16 ms throttle; let it run before the step's own render (only when one is pending).
		if (ui.renderRequested || ui.renderTimer) await sleep(30);
		let error = null;
		try { ui.renderNow(!!step.force); } catch (e) { error = String(e && e.message).slice(0, 80); }
		sc.log.push({ fullRedraws: ui.fullRedraws, out, rendered: specs.map((s) => s.rendered), overlays: overlays.map((o) => o.spec.rendered), error });
		if (error) break;
	}
	// The exit: the document dumped to the main screen (or the screen kept), then the capabilities restored.
	out = "";
	sc.preserveScreen = sc.preserve;
	ui.stop({ preserveScreen: sc.preserve });
	sc.stopOut = out;
	sc.imagesAfterStop = getCapabilities().images ?? "";
}
process.stdout.write(JSON.stringify(scenarios) + "\n", () => process.exit(0)); // pending flash timers would otherwise keep Pi alive
