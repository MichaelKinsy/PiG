export { dispatchProviderObject as dispatchNativeProvider } from "./provider-object.mjs";

export function nativeDeclaration(provider, key) {
  const paths = [
    "getModels", "filterModels", "refreshModels", "stream", "streamSimple", "fetchDeferred", "cancelDeferred", "generateImages", "classify",
    "auth.apiKey.check", "auth.apiKey.resolve", "auth.apiKey.login", "auth.oauth.login", "auth.oauth.refresh", "auth.oauth.toAuth",
  ];
  const methods = paths.filter(path => typeof path.split(".").reduce((object, name) => object?.[name], provider) === "function");
  const auth = {};
  for (const kind of ["apiKey", "oauth"]) {
    const method = provider.auth[kind];
    if (method) auth[kind] = {
      name: method.name,
      ...(Object.hasOwn(method, "isSubscription") ? { isSubscription: method.isSubscription } : {}),
      ...(Object.hasOwn(method, "loginLabel") ? { loginLabel: method.loginLabel } : {}),
    };
  }
  return {
    id: provider.id, key, name: provider.name, baseUrl: provider.baseUrl, headers: provider.headers, auth, methods,
    models: provider.getModels(),
    oauth: provider.auth.oauth ? {
      name: provider.auth.oauth.name, isSubscription: provider.auth.oauth.isSubscription,
      has_login: typeof provider.auth.oauth.login === "function", has_refresh: true, has_get_api_key: true,
    } : undefined,
  };
}
