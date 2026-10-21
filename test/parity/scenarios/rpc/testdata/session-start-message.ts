// A command starts a new Session. The new Session's session_start listener sends a custom message at once, as a Pigpen warden's reset does after its async scan, and defers a toast with a zero-delay timer.
export default function (pi: any) {
	pi.registerCommand("fresh", {
		description: "Start a new Session",
		handler: async (_args: string, ctx: any) => {
			await ctx.newSession();
		},
	});
	pi.on("session_start", async (event: any, ctx: any) => {
		if (event.reason !== "new") return;
		pi.sendMessage({ customType: "session-start-now", content: "now", display: true });
		setTimeout(() => {
			ctx.ui.notify("session-start-later", "info");
		}, 0);
	});
}
