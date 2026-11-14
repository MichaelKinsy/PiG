// Registers a provider whose api no API implementation is registered for, and no streamSimple of its own.
export default function (pi) {
	pi.registerProvider("extp", {
		baseUrl: process.env.UNKNOWN_API_BASE_URL,
		apiKey: "k",
		api: "nosuchapi",
		models: [
			{
				id: "m",
				name: "m",
				reasoning: false,
				input: ["text"],
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
				contextWindow: 1000,
				maxTokens: 100,
			},
		],
	});
}
