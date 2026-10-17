// A provider object whose OAuth login sends an info event with two links (one labelled), then a progress event, then asks a text prompt.
const model = { id: "info-model", name: "Info Model", provider: "info-node", api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 100 };

export default (pi) =>
  pi.registerProvider({
    id: "info-node",
    name: "Info Node",
    auth: {
      oauth: {
        name: "Info Node account",
        login: async (interaction) => {
          interaction.notify({ type: "info", message: "Use your work account", links: [{ url: "https://example.invalid/help", label: "Help" }, { url: "https://example.invalid/terms" }] });
          interaction.notify({ type: "progress", message: "Checking the workspace" });
          const code = await interaction.prompt({ type: "text", message: "Paste the sign-in code" });
          return { type: "oauth", access: code, refresh: "r", expires: 4102444800000 };
        },
        refresh: async (credential) => credential,
        toAuth: async (credential) => ({ apiKey: credential.access }),
      },
    },
    getModels: () => [model],
    stream() { throw new Error("unused"); },
    streamSimple() { throw new Error("unused"); },
  });
