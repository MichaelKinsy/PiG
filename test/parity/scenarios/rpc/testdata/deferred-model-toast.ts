// Defers a toast with setTimeout(0) from model_select, as @liyu1981/pi-tweaks' remember-model does, and picks a model from session_start so the toast is scheduled inside a Session replacement.
export default function (pi: any) {
	pi.on("session_start", async (event: any, ctx: any) => {
		if (event.reason !== "new") return;
		const model = ctx.modelRegistry.find("fixture", "model-two");
		if (model) await pi.setModel(model);
	});
	pi.on("model_select", async (event: any, ctx: any) => {
		await Promise.resolve();
		setTimeout(() => {
			ctx.ui.notify(`deferred:${event.model.id}`, "info");
		}, 0);
	});
}
