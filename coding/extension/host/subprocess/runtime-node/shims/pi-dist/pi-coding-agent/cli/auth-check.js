import { resolveCliModel } from "../core/model-resolver.js";
import { ModelRuntime } from "../core/model-runtime.js";
import { InMemoryCodingAgentModelsStore } from "../core/models-store.js";
import { AuthCommandError, getAuthCredential, validateAuthCommandArgs } from "./auth-command.js";
export async function checkProviderAuth(args, modelRuntime, options = { refresh: false }) {
    const { provider: cliProvider, model: cliModel } = validateAuthCommandArgs(args, "check");
    let provider = cliProvider;
    if (cliModel) {
        const resolved = resolveCliModel({ cliProvider, cliModel, modelRuntime });
        if (resolved.error || !resolved.model) {
            throw new AuthCommandError(resolved.error ?? `Unable to resolve model "${cliModel}"`);
        }
        provider = resolved.model.provider;
    }
    if (!provider)
        throw new AuthCommandError("Unable to resolve an auth provider");
    if (modelRuntime.getError()) {
        return { status: "invalid", provider, reason: "invalid_state" };
    }
    if (!modelRuntime.getProvider(provider)) {
        return { status: "not_ready", provider, reason: "provider_not_found" };
    }
    try {
        const auth = await modelRuntime.checkAuth(provider);
        if (!auth)
            return { status: "not_ready", provider, reason: "credentials_not_configured" };
        if (options.refresh && !(await modelRuntime.getAuth(provider))) {
            return { status: "not_ready", provider, reason: "credentials_not_configured" };
        }
        return { status: "ready", provider, authType: auth.type };
    }
    catch {
        return { status: "invalid", provider, reason: "invalid_state" };
    }
}
export async function getProviderCredential(providerId, modelRuntime, credentials, options) {
    const credential = await credentials.read(providerId);
    if (!options.refresh && credential?.type === "oauth")
        return credential.access;
    return getAuthCredential(await modelRuntime.getAuth(providerId));
}
export async function createAuthCheckModelRuntime(credentials) {
    return ModelRuntime.create({
        credentials,
        modelsStore: new InMemoryCodingAgentModelsStore(),
        allowModelNetwork: false,
        refreshOnCreate: false,
    });
}
//# sourceMappingURL=auth-check.js.map