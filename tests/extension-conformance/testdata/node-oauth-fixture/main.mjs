// Registers an OAuth provider through the upstream config.oauth shape. The same
// file loads on upstream pi (in-process) and on pig (bridged through the node
// runtime), so it drives both the node OAuth bridge test and the pi-vs-pig
// parity scenario. login/refreshToken/getApiKey mirror the Go/Rust/Python
// conformance fixtures; config.oauth has no credential store (upstream does not
// define one), so this provider is a store-less OAuth provider.
export default function (pi) {
  pi.registerProvider("conformance-oauth", {
    name: "Conformance OAuth",
    oauth: {
      name: "Conformance OAuth",
      isSubscription: true,
      async login(callbacks) {
        callbacks.onDeviceCode({ userCode: "CONF-USER-CODE", verificationUri: "https://conf.example/verify" });
        callbacks.onProgress?.("waiting");
        const value = await callbacks.onPrompt({ message: "paste the code" });
        return { access: "access-" + value, refresh: "refresh-tok", expires: 4242 };
      },
      async refreshToken(creds) {
        return { access: "refreshed-" + creds.refresh, refresh: creds.refresh, expires: 9999 };
      },
      getApiKey(creds) {
        if (creds.access === "boom") throw new Error("getApiKey exploded");
        return "key:" + creds.access;
      },
    },
  });
}
