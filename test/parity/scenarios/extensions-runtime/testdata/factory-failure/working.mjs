export default function (pi) {
  pi.registerProvider("working-provider", { baseUrl: "https://provider.test/v1", apiKey: "provider-test-key" });
}
