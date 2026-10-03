/*
 * Adapted from modelcontextprotocol/typescript-sdk v1.29.0.
 * Copyright (c) 2024 Anthropic, PBC. Licensed under MIT; see LICENSES/.
 * Modified to use dependency-free structural validation.
 */
import { isObject } from "../protocol/jsonrpc.js";
function object(value, name) {
    if (!isObject(value))
        throw new Error(`Invalid ${name}`);
    return value;
}
/** Drops `undefined` values so optional fields are absent rather than present-but-undefined. */
function compact(value) {
    return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined));
}
function requiredString(value, name) {
    if (typeof value !== "string" || value.length === 0)
        throw new Error(`Invalid ${name}`);
    return value;
}
/** Treats `null` and `""` as absent: servers send them for fields they have no value for, like `scope: ""`. */
function absent(value) {
    return value === undefined || value === null || value === "";
}
function optionalString(value, name) {
    if (absent(value))
        return undefined;
    return requiredString(value, name);
}
function optionalStrings(value, name) {
    if (value === undefined || value === null)
        return undefined;
    if (!Array.isArray(value) || value.some((item) => typeof item !== "string"))
        throw new Error(`Invalid ${name}`);
    return [...value];
}
function safeUrl(value, name) {
    const text = requiredString(value, name);
    // URL parsing throws a `TypeError`, which discovery reserves for network failures.
    if (!URL.canParse(text))
        throw new Error(`Invalid ${name}`);
    const url = new URL(text);
    if (["javascript:", "data:", "vbscript:"].includes(url.protocol))
        throw new Error(`Invalid ${name}`);
    return text;
}
function optionalUrl(value, name) {
    return absent(value) ? undefined : safeUrl(value, name);
}
export function parseProtectedResourceMetadata(value) {
    const input = object(value, "OAuth protected resource metadata");
    return compact({
        ...input,
        resource: safeUrl(input.resource, "OAuth protected resource metadata resource"),
        authorization_servers: optionalStrings(input.authorization_servers, "authorization_servers")?.map((url) => safeUrl(url, "authorization server URL")),
        scopes_supported: optionalStrings(input.scopes_supported, "scopes_supported"),
    });
}
export function parseAuthorizationServerMetadata(value) {
    const input = object(value, "authorization server metadata");
    const responseTypes = optionalStrings(input.response_types_supported, "response_types_supported");
    if (!responseTypes)
        throw new Error("Invalid response_types_supported");
    return compact({
        ...input,
        issuer: safeUrl(input.issuer, "authorization server issuer"),
        authorization_endpoint: safeUrl(input.authorization_endpoint, "authorization endpoint"),
        token_endpoint: safeUrl(input.token_endpoint, "token endpoint"),
        registration_endpoint: optionalUrl(input.registration_endpoint, "registration endpoint"),
        scopes_supported: optionalStrings(input.scopes_supported, "scopes_supported"),
        response_types_supported: responseTypes,
        grant_types_supported: optionalStrings(input.grant_types_supported, "grant_types_supported"),
        token_endpoint_auth_methods_supported: optionalStrings(input.token_endpoint_auth_methods_supported, "token_endpoint_auth_methods_supported"),
        code_challenge_methods_supported: optionalStrings(input.code_challenge_methods_supported, "code_challenge_methods_supported"),
        client_id_metadata_document_supported: typeof input.client_id_metadata_document_supported === "boolean"
            ? input.client_id_metadata_document_supported
            : undefined,
        authorization_response_iss_parameter_supported: typeof input.authorization_response_iss_parameter_supported === "boolean"
            ? input.authorization_response_iss_parameter_supported
            : undefined,
    });
}
export function parseOAuthTokens(value) {
    const input = object(value, "OAuth token response");
    // `Number(null)` is 0, which would mark the token as expired at once.
    const expires = absent(input.expires_in) ? undefined : Number(input.expires_in);
    if (expires !== undefined && !Number.isFinite(expires))
        throw new Error("Invalid expires_in");
    return compact({
        access_token: requiredString(input.access_token, "access_token"),
        token_type: requiredString(input.token_type, "token_type"),
        expires_in: expires,
        scope: optionalString(input.scope, "scope"),
        refresh_token: optionalString(input.refresh_token, "refresh_token"),
        id_token: optionalString(input.id_token, "id_token"),
    });
}
export function parseClientInformation(value) {
    const input = object(value, "OAuth client registration response");
    return compact({
        ...input,
        client_id: requiredString(input.client_id, "client_id"),
        client_secret: optionalString(input.client_secret, "client_secret"),
        client_id_issued_at: typeof input.client_id_issued_at === "number" ? input.client_id_issued_at : undefined,
        client_secret_expires_at: typeof input.client_secret_expires_at === "number" ? input.client_secret_expires_at : undefined,
        redirect_uris: optionalStrings(input.redirect_uris, "redirect_uris") ?? [],
    });
}
//# sourceMappingURL=types.js.map