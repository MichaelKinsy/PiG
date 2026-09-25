// Fixture stand-in for the upstream faux provider.
export function fauxResponse(options: { stopReason?: string }) {
	return { stopReason: options.stopReason ?? "stop" };
}
