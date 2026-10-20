// A manual clock for Pi's render throttle, shared by the screen oracles.
// TuiBase draws a requested frame from setTimeout once performance.now() says 16 ms have passed since the last frame. An oracle that
// sleeps a fixed real time and assumes the frame has run races the machine's timers: when the process is descheduled between the
// sleep and Pi's own timer, the sleep ends first, the step's renderNow runs without the requested frame in front of it, and the
// recorded bytes change. Importing this module replaces setTimeout, clearTimeout and performance.now with a clock the oracle
// advances itself; runRequestedFrame then draws the pending frame exactly as the throttle's timer would, with no wall time.
let now = 1_000_000; // a Pi process has been up for longer than one throttle interval when its first frame is requested
let seq = 0;
const timers = [];

globalThis.setTimeout = (fn, ms = 0, ...args) => {
	const timer = { fn: () => fn(...args), at: now + Math.max(1, Number(ms) || 0), seq: seq++, unref() { return timer; }, ref() { return timer; }, hasRef() { return true; } };
	timers.push(timer);
	return timer;
};
globalThis.clearTimeout = (timer) => {
	const index = timers.indexOf(timer);
	if (index !== -1) timers.splice(index, 1);
};
Object.defineProperty(globalThis.performance, "now", { value: () => now, configurable: true, writable: true });

// Draw the frame `ui` has requested, if any. requestRender queues the throttle on process.nextTick, which runs before the next
// setImmediate; after that the throttle timer is the earliest one, and firing it advances the clock to its due time. A frame that
// requests another is drawn too, so the oracle continues from an idle Pi.
export async function runRequestedFrame(ui) {
	if (!ui.renderRequested && !ui.renderTimer) return;
	await new Promise((resolve) => setImmediate(resolve));
	while (ui.renderRequested || ui.renderTimer) {
		let next = -1;
		for (let i = 0; i < timers.length; i++) if (next === -1 || timers[i].at < timers[next].at || (timers[i].at === timers[next].at && timers[i].seq < timers[next].seq)) next = i;
		if (next === -1) throw new Error("Pi has a frame requested but no timer to draw it");
		const [timer] = timers.splice(next, 1);
		now = Math.max(now, timer.at);
		timer.fn();
	}
}
