// A second extension with a model_select listener that defers nothing. PiG packs it with deferred-model-toast.ts into one Node runtime process, so the first extension's timer must not fire while the host dispatches this listener.
export default function (pi: any) {
	pi.on("model_select", async () => {
		await Promise.resolve();
	});
}
