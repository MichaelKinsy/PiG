// All credentials and endpoints are local test data. No provider request runs.
export default function (pi) {
  for (const [id, subscription] of [["subscription-yes", true], ["subscription-no", false], ["subscription-omitted", undefined]]) {
    pi.registerProvider(id, {
      baseUrl: "https://subscription.invalid/v1",
      api: "openai-completions",
      models: [{ id: "label-test", name: "Label test", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 1024 }],
      oauth: {
        name: id,
        ...(subscription === undefined ? {} : { isSubscription: subscription }),
        async login() { return { access: "fixture", refresh: "fixture", expires: 4102444800000 }; },
        async refreshToken(credentials) { return credentials; },
        getApiKey(credentials) { return credentials.access; },
      },
    });
  }
}
