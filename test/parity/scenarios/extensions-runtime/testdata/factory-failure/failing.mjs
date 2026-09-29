// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8423-extension-factory-failure.test.ts:12
export default function (pi) {
  const state = globalThis.__factoryFailureProbe = { api: pi, eventCalls: 0 };
  state.unsubscribe = pi.events.on("factory-failure", () => { state.eventCalls++; });
  pi.registerFlag("failed-flag", { type: "boolean", default: true });
  state.flagDuringLoad = pi.getFlag("failed-flag");
  state.lateResult = new Promise(resolve => setImmediate(() => {
    try {
      pi.registerFlag("timer-flag", { type: "boolean", default: true });
      resolve(null);
    } catch (error) {
      resolve(error.message);
    }
  }));
  pi.unregisterProvider("working-provider");
  pi.registerProvider("failed-provider", { baseUrl: "https://provider.test/v1", apiKey: "provider-test-key" });
  throw new Error("factory failed");
}
