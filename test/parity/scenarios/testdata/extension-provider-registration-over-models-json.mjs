// Upstream ModelRuntime.registerProvider merges a registration's defined fields over
// the models.json provider configuration (model-runtime.ts:753-766;
// provider-composer.ts:208-210 configuredApiKey returns extension?.apiKey ??
// config?.apiKey). This registration defines only an api and a streamSimple callback,
// so the models.json apiKey and its models stay in effect and the provider remains
// listed by --list-models.
export default function (pi) {
  pi.registerProvider("fixture", {
    api: "openai-completions",
    streamSimple: () => {
      throw new Error("the registered fixture streamSimple must not run in this probe");
    },
  });
}
