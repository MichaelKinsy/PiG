// Draws the fullscreen frames of Pi's TuiAltScreen over the session tui/scroll_frame_spec_test.go writes for PiG (the same turns, from the
// spec file), with Pi's own components: what a wheel event, a page key and a scrollbar drag step cost, and a streamed token at the end of
// the transcript and with the viewport scrolled up. Pi's counterpart of internal/codingagent/scroll_frame_bench_test.go.
// usage: node scroll_frame_bench.mjs <pi-coding-agent package root> <spec.json> <turns> <columns> <rows> [iterations] [time|count|burst|dump] > result.json
// The package root chooses the Pi that is measured: the oracle, or a copy of it with Pi's b7bb7ca905b8 applied.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [, , rootArg, specPath, turnsArg, columnsArg, rowsArg, iterationsArg, modeArg] = process.argv;
// "time": time every frame; "count": count the render calls and lines of every component class per frame; "burst": 600 wheel events on
// real timers; "dump": what every top-level component of the transcript renders.
const mode = modeArg ?? "time";
// The clocked modes draw a requested frame at once on the manual clock of the screen oracles; burst runs on the real timers.
const runRequestedFrame = mode === "burst" ? (ui) => new Promise((resolve) => setTimeout(resolve, 40)) : (await import("./frame-clock.mjs")).runRequestedFrame;
const sleepReal = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const root = rootArg.replace(/\/?$/, "/");
const turns = JSON.parse(readFileSync(specPath, "utf8")).slice(0, Number(turnsArg));
const columns = Number(columnsArg);
const rows = Number(rowsArg);
const iterations = Number(iterationsArg ?? 300);
process.env.FORCE_COLOR = "3";
for (const name of ["TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"]) delete process.env[name];
process.env.TERM = "xterm-256color";
const load = (path) => import(pathToFileURL(root + path));
const tui = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { TuiAltScreen, Container, ScrollView, VStack, Text, Spacer, setCapabilities } = tui;
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { UserMessageComponent } = await load("dist/modes/interactive/components/user-message.js");
const { AssistantMessageComponent } = await load("dist/modes/interactive/components/assistant-message.js");
const { ToolExecutionComponent } = await load("dist/modes/interactive/components/tool-execution.js");
const { createAllToolDefinitions } = await load("dist/core/tools/index.js");
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");

const message = (blocks) => ({
	role: "assistant",
	content: blocks,
	api: "openai-completions",
	provider: "p",
	model: "m",
	usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
	stopReason: "stop",
	timestamp: 0,
});
const cwd = "/work";
const defs = createAllToolDefinitions(cwd);
const chat = new Container();
// The resume path of interactive mode (renderSessionContext): the user message, the assistant message, and the card of its tool call
// with the result, no spacer between them (a spacer before every user message but the first, as interactive mode adds) and no recorded
// duration.
turns.forEach((turn, i) => {
	if (turn.user) {
		if (chat.children.length > 0) chat.addChild(new Spacer(1));
		chat.addChild(new UserMessageComponent(turn.user));
	}
	chat.addChild(new AssistantMessageComponent(message([{ type: "thinking", thinking: turn.thinking }, { type: "text", text: turn.answer }]), false));
	const card = new ToolExecutionComponent("bash", `call_h${i}`, { command: turn.command }, { showImages: false }, defs.bash, { requestRender() {} }, cwd);
	card.setArgsComplete();
	card.updateResult({ content: [{ type: "text", text: turn.output }], isError: false }, false);
	chat.addChild(card);
});
const tail = new AssistantMessageComponent(message([]), false);
chat.addChild(new Spacer(1));
chat.addChild(tail);
const document = new Container();
document.addChild(new Text("transcript header", 0, 0));
document.addChild(chat);
const transcript = new ScrollView(document, { follow: "end", primary: true, overscroll: "chain", scrollbar: "auto" });
const root_ = new VStack(
	[
		{ component: transcript, basis: 0, grow: 1, shrink: 1, minSize: 1 },
		{ component: new Text("\x1b[2m> editor dock\x1b[0m", 0, 0), grow: 0, shrink: 1, minSize: 1 },
	],
	{},
);
let onInput;
let writes = 0;
const term = {
	columns,
	rows,
	kittyProtocolActive: false,
	start(input) { onInput = input; },
	stop() {},
	async drainInput() {},
	write() { writes++; },
	moveBy() {},
	hideCursor() {},
	showCursor() {},
	clearLine() {},
	clearFromCursor() {},
	clearScreen() {},
	setTitle() {},
	setProgress() {},
};
const ui = new TuiAltScreen(term);
ui.setLayoutRoot(root_);
ui.start();
await runRequestedFrame(ui);
const docLines = document.render(columns).length;

const stats = (samples) => {
	const sorted = [...samples].sort((a, b) => a - b);
	const mean = samples.reduce((a, b) => a + b, 0) / samples.length;
	return { n: samples.length, medianMs: sorted[Math.floor(sorted.length / 2)], meanMs: mean, p90Ms: sorted[Math.floor(sorted.length * 0.9)], minMs: sorted[0] };
};
// Render calls and the lines they return, by class, over the counted frames.
const counted = { Container: tui.Container, Text: tui.Text, Markdown: tui.Markdown, Box: tui.Box, Spacer: tui.Spacer, ScrollView: tui.ScrollView, UserMessageComponent, AssistantMessageComponent, ToolExecutionComponent };
let counts = {};
for (const [name, cls] of Object.entries(counted)) {
	const original = cls.prototype.render;
	cls.prototype.render = function (width) {
		const entry = (counts[name] ??= { calls: 0, lines: 0 });
		entry.calls++;
		const result = original.call(this, width);
		entry.lines += result.length;
		return result;
	};
}
const send = async (data) => {
	onInput(data);
	await runRequestedFrame(ui);
};
const atTop = () => transcript.scrollTop === 0;
let text = "";
const stream = async () => {
	if (text.length > 4000) text = "";
	text += "token ";
	tail.updateContent(message([{ type: "text", text }]), true);
	ui.requestRender();
	await runRequestedFrame(ui);
};
const toEnd = async () => {
	transcript.scrollToEnd();
	ui.requestRender();
	await runRequestedFrame(ui);
};
let dragY = 3;
let dragStep = 1;
const kinds = [
	["wheel", toEnd, async () => {
		await send(`\x1b[<64;10;${Math.floor(rows / 2)}M`);
		if (atTop()) { transcript.scrollToEnd(); await runRequestedFrame(ui); }
	}],
	["page", toEnd, async () => {
		await send("\x1b[5~");
		if (atTop()) { transcript.scrollToEnd(); await runRequestedFrame(ui); }
	}],
	["drag", async () => { await toEnd(); await send(`\x1b[<0;${columns};${Math.floor(rows / 2)}M`); }, async () => {
		dragY += dragStep;
		if (dragY >= rows - 4 || dragY <= 3) dragStep = -dragStep;
		await send(`\x1b[<32;${columns};${dragY}M`);
	}],
	["content-bottom", toEnd, stream],
	["content-scrolled-up", async () => { await send(`\x1b[<0;${columns};${Math.floor(rows / 2)}m`); transcript.scrollTo(Math.floor(docLines / 2)); ui.requestRender(); await runRequestedFrame(ui); }, stream],
];
const results = {};
if (mode === "dump") {
	// What every top-level component of the transcript renders, for a line-by-line comparison with PiG's TestDumpScrollFrameTurns.
	const strip = (line) => line.replace(/\x1b\[[0-9;:]*[A-Za-z]/g, "").replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, "");
	const children = chat.children.map((child) => ({ type: child.constructor.name, lines: [...child.render(columns)].map(strip) }));
	process.stdout.write(JSON.stringify({ columns, children }) + "\n", () => process.exit(0));
	await new Promise(() => {});
}
if (mode === "burst") {
	// 600 wheel events 1 ms apart (50 up, 50 down) on the real frame timer: how many frames are drawn, and the CPU they cost.
	const sleep = sleepReal;
	await toEnd();
	for (let i = 0; i < 20; i++) await send(`\x1b[<64;10;${Math.floor(rows / 2)}M`);
	await toEnd();
	await sleep(100);
	writes = 0;
	const cpu0 = process.cpuUsage();
	const wall0 = process.hrtime.bigint();
	for (let i = 0; i < 600; i++) {
		onInput(`\x1b[<${64 + (Math.floor(i / 50) % 2)};10;${Math.floor(rows / 2)}M`); // 50 events up, 50 down
		await sleep(1);
	}
	await sleep(100);
	const cpu = process.cpuUsage(cpu0);
	const wallMs = Number(process.hrtime.bigint() - wall0) / 1e6;
	process.stdout.write(JSON.stringify({ turns: turns.length, columns, rows, docLines, mode, burst: { events: 600, writes, wallMs, cpuMs: (cpu.user + cpu.system) / 1000 } }) + "\n", () => process.exit(0));
	await new Promise(() => {});
}
for (const [name, setup, step] of kinds) {
	await setup();
	for (let i = 0; i < 20; i++) await step(i); // warm up the JIT and every cache
	if (mode === "count") {
		counts = {};
		const frames = 100;
		for (let i = 0; i < frames; i++) await step(i);
		const perFrame = {};
		for (const [cls, entry] of Object.entries(counts)) perFrame[cls] = [entry.calls / frames, entry.lines / frames];
		results[name] = perFrame;
	} else {
		const samples = [];
		for (let i = 0; i < iterations; i++) {
			const start = process.hrtime.bigint();
			await step(i);
			samples.push(Number(process.hrtime.bigint() - start) / 1e6);
		}
		results[name] = stats(samples);
	}
}
process.stdout.write(JSON.stringify({ turns: turns.length, columns, rows, docLines, iterations, mode, results }) + "\n", () => process.exit(0));
