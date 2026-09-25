/**
 * chain: run a task as a chain of agent steps (scout -> plan -> build -> test),
 * with a live progress widget above the editor.
 *
 *   /chain fix the failing tests
 *
 * Plain Pi extension API (registerCommand, on, sendUserMessage, ui.setWidget,
 * ui.notify). The same file loads unchanged in Pi (`pi -e ./chain`) and in PiG
 * (`pig -e ./chain`).
 */
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const STEPS = [
	{ name: "scout", prompt: (t: string) => `Scout the repository for code relevant to: ${t}. Report findings only.` },
	{ name: "plan", prompt: (t: string) => `Write a short numbered plan to: ${t}.` },
	{ name: "build", prompt: (t: string) => `Implement the plan to: ${t}. Make the smallest correct change.` },
	{ name: "test", prompt: (t: string) => `Run the tests and report the result for: ${t}.` },
];
const SPIN = ["◐", "◓", "◑", "◒"];

type State = { task: string; step: number; running: boolean; t0: number; frame: number; timer?: ReturnType<typeof setInterval> };

export default function chain(pi: ExtensionAPI) {
	let state: State | undefined;
	let ui: ExtensionContext["ui"] | undefined;

	const color = (token: string, text: string) => (ui?.theme ? ui.theme.fg(token as never, text) : text);

	function render() {
		if (!state || !ui) return;
		const parts = STEPS.map((s, i) => {
			if (i < state!.step) return color("success", `✓ ${s.name}`);
			if (i === state!.step && state!.running) return color("accent", `${SPIN[state!.frame % SPIN.length]} ${s.name}`);
			return color("dim", `○ ${s.name}`);
		});
		const secs = ((Date.now() - state.t0) / 1000).toFixed(1);
		ui.setWidget("chain", [` ${color("accent", "⛓ chain")}  ${parts.join(color("dim", " ── "))}   ${color("muted", `${secs}s`)}`]);
	}

	function send(step: number) {
		const s = STEPS[step];
		pi.sendUserMessage(`[chain ${step + 1}/${STEPS.length} · ${s.name}] ${s.prompt(state!.task)}`, { deliverAs: "followUp" });
	}

	function finish(ctx: ExtensionContext) {
		if (!state) return;
		clearInterval(state.timer);
		const secs = ((Date.now() - state.t0) / 1000).toFixed(1);
		state.running = false;
		render();
		ctx.ui.notify(`chain finished: ${STEPS.length} steps in ${secs}s`, "info");
		state = undefined;
	}

	pi.registerCommand("chain", {
		description: "Run a task as a chain: scout -> plan -> build -> test",
		handler: async (args, ctx) => {
			const task = args.trim();
			if (!task) {
				ctx.ui.notify("Usage: /chain <task>", "warning");
				return;
			}
			if (state) {
				ctx.ui.notify("A chain is already running", "warning");
				return;
			}
			ui = ctx.ui;
			state = { task, step: 0, running: true, t0: Date.now(), frame: 0 };
			state.timer = setInterval(() => {
				if (!state) return;
				state.frame++;
				render();
			}, 150);
			render();
			send(0);
		},
	});

	pi.on("agent_start", () => {
		if (state) {
			state.running = true;
			render();
		}
	});

	pi.on("agent_end", (_event, ctx) => {
		if (!state) return;
		state.step++;
		state.running = false;
		if (state.step >= STEPS.length) return finish(ctx);
		render();
		send(state.step);
	});

	pi.on("session_shutdown", () => {
		if (state) clearInterval(state.timer);
		state = undefined;
	});
}
