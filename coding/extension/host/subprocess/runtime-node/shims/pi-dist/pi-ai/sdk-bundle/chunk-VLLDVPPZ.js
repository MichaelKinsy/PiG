import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/auth/helpers.js
function envApiKeyAuth(name, envVars) {
  return {
    name,
    login: /* @__PURE__ */ __name(async (interaction) => {
      interaction.signal.throwIfAborted();
      const key = await interaction.prompt({ type: "secret", message: `Enter ${name}` });
      interaction.signal.throwIfAborted();
      return { type: "api_key", key };
    }, "login"),
    resolve: /* @__PURE__ */ __name(async ({ ctx, credential, signal }) => {
      signal.throwIfAborted();
      if (credential?.key) {
        return { auth: { apiKey: credential.key }, env: credential.env, source: "stored credential" };
      }
      for (const envVar of envVars) {
        const value = await ctx.env(envVar);
        signal.throwIfAborted();
        if (value)
          return { auth: { apiKey: value }, source: envVar };
      }
      return void 0;
    }, "resolve")
  };
}
__name(envApiKeyAuth, "envApiKeyAuth");
function lazyOAuth(input) {
  let promise;
  const loaded = /* @__PURE__ */ __name(() => {
    promise ??= input.load();
    return promise;
  }, "loaded");
  return {
    name: input.name,
    isSubscription: input.isSubscription,
    loginLabel: input.loginLabel,
    login: /* @__PURE__ */ __name(async (interaction, options) => (await loaded()).login(interaction, options), "login"),
    refresh: /* @__PURE__ */ __name(async (credential, signal) => (await loaded()).refresh(credential, signal), "refresh"),
    toAuth: /* @__PURE__ */ __name(async (credential) => (await loaded()).toAuth(credential), "toAuth")
  };
}
__name(lazyOAuth, "lazyOAuth");

export {
  envApiKeyAuth,
  lazyOAuth
};
