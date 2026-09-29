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
        return { access: "access-" + value, refresh: "refresh-tok", expires: 4242, accountId: "account-login", scope: "scope-login" };
      },
      async refreshToken(creds) {
        return { access: "refreshed-" + creds.refresh, refresh: creds.refresh, expires: 9999, accountId: creds.accountId, scope: creds.scope };
      },
      getApiKey(creds) {
        if (creds.access === "boom") throw new Error("getApiKey exploded");
        return "key:" + creds.access;
      },
    },
  });
  // Credentials are Pi's complete token object: a fractional expiry and provider-owned keys survive, and refresh spreads its input. Behavior matches the Go, Rust and Python fixtures.
  pi.registerProvider("conformance-oauth-object", {
    name: "Conformance OAuth Object",
    oauth: {
      name: "Conformance OAuth Object",
      async login() {
        return { access: "object-access", refresh: "object-refresh", expires: 1700000000000.25, meta: { k: [1, null, ""] }, projectId: "" };
      },
      async refreshToken(creds) {
        return { ...creds, access: "refreshed-" + creds.refresh, expires: creds.expires + 0.5 };
      },
      getApiKey(creds) {
        return `key:${creds.type === "oauth" ? "typed" : "untyped"}:${"meta" in creds ? "meta" : "nometa"}`;
      },
    },
  });
  // JSON.parse reads the wire digits 1152921504606847000 as the double 2**60, so getApiKey reports a distance of 0 from 2**60. Behavior matches the Go, Rust and Python fixtures.
  pi.registerProvider("conformance-oauth-large", {
    name: "Conformance OAuth Large",
    oauth: {
      name: "Conformance OAuth Large",
      async login() {
        return { access: "large-access", refresh: "large-refresh", expires: 2 ** 60 };
      },
      getApiKey(creds) {
        return `key:${creds.expires - 2 ** 60}:${BigInt(creds.expires) - 2n ** 60n}`;
      },
    },
  });
  // Pi passes refreshToken an AbortSignal (auth/resolve.ts:149-153) and login the interaction signal (provider-composer.ts:288); a retained signal stays live after the callback returns.
  let retained;
  pi.registerProvider("conformance-oauth-signal", {
    name: "Conformance OAuth Signal",
    oauth: {
      name: "Conformance OAuth Signal",
      async login(callbacks) {
        return { access: `login-signal:${callbacks.signal instanceof AbortSignal}:${callbacks.signal?.aborted}`, refresh: "r", expires: 1 };
      },
      async refreshToken(creds, signal) {
        if (creds.refresh === "hang") {
          await new Promise((resolve, reject) => signal.addEventListener("abort", () => { retained = signal; reject(signal.reason); }, { once: true }));
        }
        retained = signal;
        return { ...creds, access: `refresh-signal:${signal instanceof AbortSignal}:${signal.aborted}` };
      },
      getApiKey(creds) {
        if (creds.access?.startsWith("reason:")) return `reason:${retained?.aborted}:${retained?.reason?.name ?? retained?.reason}`;
        return `retained:${retained instanceof AbortSignal}:${retained?.aborted}`;
      },
    },
  });
}
