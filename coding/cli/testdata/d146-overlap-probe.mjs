// Records every session_start and session_shutdown with the Session id the handler's ctx reports, one JSON line each, in the file D146_LOG.
// The first session_start that is not the startup one waits D146_HOLD_MS (the handler of the first replacement's Session: the longer one); D146_TOUCH=1 then
// uses the captured pi after the wait (pi.appendEntry), which Pi refuses with the stale-context error when the Session was replaced meanwhile.
import { appendFileSync, existsSync, writeFileSync } from "node:fs";

const log = process.env.D146_LOG;
const hold = Number(process.env.D146_HOLD_MS || 0);
const touch = process.env.D146_TOUCH === "1";

export default function (pi) {
	const record = (entry) => appendFileSync(log, JSON.stringify(entry) + "\n");
	const sid = (ctx) => {
		try {
			return ctx.sessionManager.getSessionId();
		} catch (e) {
			return "ERR:" + String(e.message).slice(0, 40);
		}
	};
	pi.on("session_start", async (event, ctx) => {
		record({ ev: "session_start", reason: event.reason, phase: "enter", sid: sid(ctx) });
		let held = false;
		if (event.reason !== "startup" && !existsSync(log + ".held")) {
			writeFileSync(log + ".held", "1");
			held = true;
			await new Promise((resolve) => setTimeout(resolve, hold));
		}
		record({ ev: "session_start", reason: event.reason, phase: "exit", sid: sid(ctx) });
		if (held && touch) {
			pi.appendEntry("d146-late", { n: 1 });
		}
	});
	pi.on("session_shutdown", async (event, ctx) => {
		record({ ev: "session_shutdown", reason: event.reason, sid: sid(ctx) });
	});
}
