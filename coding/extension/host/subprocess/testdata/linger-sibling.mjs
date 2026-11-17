// A live extension beside the one a reload removes: it observes what the host still sends to extensions and unregisters the removed extension's provider on request.
export default function (pi) {
  pi.registerCommand("linger-unregister", { description: "unregister the lingering provider", handler: async () => { pi.unregisterProvider("linger-provider"); } });
  pi.registerCommand("linger-takeover", {
    description: "register the lingering provider's name again",
    handler: async () => {
      pi.registerProvider("linger-provider", {
        api: "linger-api", baseUrl: "https://takeover.invalid", apiKey: "k",
        models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }],
      });
    },
  });
}
