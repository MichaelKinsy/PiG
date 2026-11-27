import { ownedSlice } from "../utils/text.js";
export const DEFAULT_RADIUS_GATEWAY = "https://radius.pi.dev";
function isRadiusGatewayClassifierModel(value) {
    if (typeof value !== "object" || value === null || Array.isArray(value))
        return false;
    const model = value;
    return (typeof model.id === "string" &&
        typeof model.name === "string" &&
        Array.isArray(model.input) &&
        typeof model.cost === "object" &&
        model.cost !== null &&
        !Array.isArray(model.cost) &&
        typeof model.contextWindow === "number");
}
function isRadiusGatewayModel(value) {
    if (!isRadiusGatewayClassifierModel(value))
        return false;
    const model = value;
    return typeof model.reasoning === "boolean" && typeof model.maxTokens === "number";
}
function sanitizeRadiusGatewayConfig(config) {
    if (typeof config !== "object" || config === null || Array.isArray(config))
        return undefined;
    const { baseUrl, models, classifiers } = config;
    if (typeof baseUrl !== "string" || !Array.isArray(models))
        return undefined;
    return {
        baseUrl,
        models: models.filter(isRadiusGatewayModel).map((model) => ({ ...model })),
        ...(Array.isArray(classifiers)
            ? { classifiers: classifiers.filter(isRadiusGatewayClassifierModel).map((model) => ({ ...model })) }
            : {}),
    };
}
export function normalizeRadiusGatewayUrl(value) {
    const withScheme = /^https?:\/\//iu.test(value) ? value : `https://${value}`;
    return withScheme.replace(/\/+$/u, "");
}
export function getRadiusCredentialConfig(credential) {
    return sanitizeRadiusGatewayConfig(credential?.gatewayConfig);
}
export function getRadiusModelsFromConfig(providerId, config) {
    return config.models.map((model) => ({
        ...model,
        api: "pi-messages",
        provider: providerId,
        baseUrl: config.baseUrl,
    }));
}
export function getRadiusAllModelsFromConfig(providerId, config) {
    return [
        ...getRadiusModelsFromConfig(providerId, config),
        ...(config.classifiers ?? []).map((model) => ({
            ...model,
            type: "classifier",
            api: "pi-decision",
            provider: providerId,
            baseUrl: config.baseUrl,
        })),
    ];
}
export function getRadiusModels(providerId, credential) {
    const config = getRadiusCredentialConfig(credential);
    return config ? getRadiusModelsFromConfig(providerId, config) : [];
}
function truncateHttpBody(body) {
    const trimmed = body.trim();
    return trimmed.length > 512 ? `${ownedSlice(trimmed, 0, 512)}…` : trimmed;
}
export async function loadRadiusGatewayConfig(gateway, apiKey, signal) {
    const headers = { accept: "application/json" };
    if (apiKey)
        headers.authorization = `Bearer ${apiKey}`;
    const response = await fetch(new URL("/v1/config", gateway), { headers, signal });
    if (!response.ok) {
        throw new Error(`Could not load Radius config from ${gateway}: ${response.status}: ${truncateHttpBody(await response.text())}`);
    }
    const config = sanitizeRadiusGatewayConfig(await response.json());
    if (!config)
        throw new Error(`Invalid Radius config from ${gateway}`);
    return config;
}
//# sourceMappingURL=radius-config.js.map