// A provider object whose auth declares an OAuth login with a loginLabel and a
// select prompt in its flow, and its own API-key login asking two prompts.
// wiring-go/extension.go registers the same provider through the Go SDK.
const model = (provider) => ({ id: "wiring-model", name: "Wiring Model", provider, api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 100 });

export function registerWiringProvider(pi, id, name) {
  pi.registerProvider({
    id,
    name,
    auth: {
      oauth: {
        name: `${name} account`,
        loginLabel: `${name} SSO`,
        login: async (interaction) => {
          const region = await interaction.prompt({ type: "select", message: "Choose a region", options: [{ id: "eu", label: "Europe" }, { id: "us", label: "United States" }] });
          const code = await interaction.prompt({ type: "text", message: "Paste the sign-in code" });
          return { access: `${code}-${region}`, expires: 4102444800000, refresh: `refresh-${region}`, type: "oauth" };
        },
        refresh: async (credential) => credential,
        toAuth: async (credential) => ({ apiKey: credential.access }),
      },
      apiKey: {
        name: `${name} key`,
        resolve: async ({ credential }) => (credential?.key ? { auth: { apiKey: credential.key } } : undefined),
        login: async (interaction) => {
          const baseUrl = await interaction.prompt({ type: "text", message: "Gateway URL" });
          const key = await interaction.prompt({ type: "secret", message: "Gateway key" });
          return { env: { WIRING_GATEWAY_URL: baseUrl }, key, type: "api_key" };
        },
      },
    },
    getModels: () => [model(id)],
    stream() { throw new Error("unused"); },
    streamSimple() { throw new Error("unused"); },
  });
}
