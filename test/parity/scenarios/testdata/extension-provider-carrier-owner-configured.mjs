// The carrier owner (extension-provider-carrier-owner.mjs) registering its Provider with refreshModelsAuth "configured" (ai/src/models.ts:204): the catalog refresh
// publishes a model naming whether it received a credential, so a refresh that was handed one (the "credential" default) is told from one that was not.
import owner from "./extension-provider-carrier-owner.mjs";

export default function (pi) {
  const registerProvider = pi.registerProvider.bind(pi);
  const extra = [];
  const configured = provider => {
    const list = base => () => [...base(), ...extra];
    return {
      ...provider,
      refreshModelsAuth: "configured",
      getModels: list(provider.getModels),
      getAllModels: list(provider.getAllModels),
      refreshModels: async context => {
        if (!context.force) return;
        await context.publish({ persist: null, update: () => { extra.push({ ...provider.getModels()[0], id: `refreshed|credential=${context.credential != null}` }); } });
      },
    };
  };
  owner(new Proxy(pi, {
    get: (target, key) => key === "registerProvider" ? provider => registerProvider(configured(provider)) : typeof target[key] === "function" ? target[key].bind(target) : target[key],
  }));
}
