import { PI_AI_VERSION } from "./version.js";
import { pigUserAgent } from "../../../pig-identity.mjs";
/** pi's attribution, used by pi-agent-core, pi-durable, and the coding agent. */
export const PI_ATTRIBUTION = { name: "PiG", url: "https://github.com/MichaelKinsy/PiG", categories: ["cli-agent"] };
const NVIDIA_NIM_HOST = "integrate.api.nvidia.com";
const OPENCODE_HOST = "opencode.ai";
/** Merge identities field by field; later sources win and undefined fields are skipped. */
export function mergeClientIdentity(...sources) {
    let merged;
    for (const source of sources) {
        if (!source)
            continue;
        merged = { ...merged };
        if (source.userAgent !== undefined)
            merged.userAgent = source.userAgent;
        if (source.attribution !== undefined)
            merged.attribution = source.attribution;
        if (source.version !== undefined)
            merged.version = source.version;
    }
    return merged;
}
/** The version in the detailed `User-Agent`. */
export function getClientVersion(identity) {
    return identity?.version ?? PI_AI_VERSION;
}
function runtimeDescription() {
    if (typeof process === "undefined")
        return undefined;
    const versions = process.versions;
    if (versions?.bun)
        return `bun/${versions.bun}`;
    if (versions?.node)
        return `node/${process.version}`;
    return undefined;
}
/** The `User-Agent` value of an identity. */
export function formatUserAgent(identity) {
    const mode = identity?.userAgent ?? "detailed";
    if (typeof mode === "object")
        return mode.custom;
// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
    if (mode === "short")
        return "pig";
    return pigUserAgent("pig", identity?.version);
}
function matchesHost(baseUrl, expectedHost) {
    try {
        return new URL(baseUrl).hostname === expectedHost;
    }
    catch {
        return false;
    }
}
function isOpenRouter(target) {
    return target.provider === "openrouter" || target.baseUrl.includes("openrouter.ai");
}
function isNvidiaNim(target) {
    return target.provider === "nvidia" || matchesHost(target.baseUrl, NVIDIA_NIM_HOST);
}
function isOpenCode(target) {
    return (target.provider === "opencode" || target.provider === "opencode-go" || matchesHost(target.baseUrl, OPENCODE_HOST));
}
/** Provider attribution headers of an identity for a request target. Empty when attribution is off. */
export function getAttributionHeaders(target, identity) {
    const attribution = identity?.attribution;
    if (!attribution)
        return {};
    const { name, url, categories } = attribution;
    if (isOpenRouter(target)) {
        return {
            "HTTP-Referer": url,
            "X-OpenRouter-Title": name,
            ...(categories === undefined || categories.length === 0
                ? {}
                : { "X-OpenRouter-Categories": categories.join(",") }),
        };
    }
    // NVIDIA expects a capitalized origin, such as `Pi`.
    if (isNvidiaNim(target))
        return { "X-BILLING-INVOKE-ORIGIN": name.charAt(0).toUpperCase() + name.slice(1) };
    if (isOpenCode(target))
        return { "x-opencode-client": name };
    return {};
}
/**
 * OpenCode's per-conversation routing header for requests to OpenCode, including custom providers pointing at it.
 * Undefined without a session or for other targets.
 */
export function getOpenCodeSessionHeaders(target, sessionId) {
    return sessionId && isOpenCode(target) ? { "x-opencode-session": sessionId } : undefined;
}
/**
 * Default headers an identity adds to a request: `User-Agent` and provider attribution. APIs apply them below model,
 * provider, and request headers, so configured headers override them and `null` request headers remove them.
 */
export function getIdentityHeaders(target, identity) {
    return { "User-Agent": formatUserAgent(identity), ...getAttributionHeaders(target, identity) };
}
function isMutableModels(models) {
    return "setProvider" in models && "deleteProvider" in models && "clearProviders" in models;
}
export function withClientIdentity(models, identity) {
    const withIdentity = (options) => ({ ...options, identity: mergeClientIdentity(identity, options?.identity) });
    const wrapped = {
        getProviders: () => models.getProviders(),
        getProvider: (id) => models.getProvider(id),
        getModels: (provider) => models.getModels(provider),
        getModel: (provider, id) => models.getModel(provider, id),
        getModelsOfType: (type, provider) => models.getModelsOfType(type, provider),
        getModelOfType: (type, provider, id) => models.getModelOfType(type, provider, id),
        getAllModels: (provider) => models.getAllModels(provider),
        refresh: (options) => models.refresh(options),
        checkAuth: (providerId, options) => models.checkAuth(providerId, options),
        getAvailable: (providerId, options) => models.getAvailable(providerId, options),
        getAvailableOfType: (type, providerId, options) => models.getAvailableOfType(type, providerId, options),
        getAllAvailable: (providerId, options) => models.getAllAvailable(providerId, options),
        getAuth: models.getAuth.bind(models),
        login: (providerId, type, interaction, options) => models.login(providerId, type, interaction, options),
        logout: (providerId, options) => models.logout(providerId, options),
        stream: (model, context, options) => models.stream(model, context, withIdentity(options)),
        complete: (model, context, options) => models.complete(model, context, withIdentity(options)),
        streamSimple: (model, context, options) => models.streamSimple(model, context, withIdentity(options)),
        completeSimple: (model, context, options) => models.completeSimple(model, context, withIdentity(options)),
        streamDeferred: (model, handle, options) => models.streamDeferred(model, handle, withIdentity(options)),
        fetchDeferred: (model, handle, options) => models.fetchDeferred(model, handle, withIdentity(options)),
        cancelDeferred: (model, handle, options) => models.cancelDeferred(model, handle, withIdentity(options)),
        generateImages: (model, context, options) => models.generateImages(model, context, withIdentity(options)),
        classify: (model, context, options) => models.classify(model, context, withIdentity(options)),
    };
    if (!isMutableModels(models))
        return wrapped;
    const mutable = {
        ...wrapped,
        setProvider: (provider) => models.setProvider(provider),
        deleteProvider: (id) => models.deleteProvider(id),
        clearProviders: () => models.clearProviders(),
    };
    return mutable;
}
//# sourceMappingURL=identity.js.map