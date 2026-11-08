/**
 * The OAuth client that sign-in and refreshes use for one MCP server: its settings, how it identifies
 * itself (a pre-registered client, a Client ID Metadata Document, or dynamic client registration), and
 * the `McpOAuthProvider` built from them.
 */
import { createHash } from "node:crypto";
import { McpOAuthProvider } from "./provider.js";
/** An `McpOAuthProvider` for the client, redirecting to `redirectUrl` or the document's variant of it. */
export function createOAuthClientProvider(options, redirectUrl, onRedirect = () => { }) {
    const { serverUrl, settings, clientMetadataDocuments: documents } = options;
    return new McpOAuthProvider({
        serverUrl,
        redirectUrl,
        clientMetadata: { client_name: settings.clientName ?? options.clientName },
        clientMetadataDocument: offersClientMetadataDocument(settings, documents, redirectUrl)
            ? (metadata) => {
                if (!documents?.listsRedirectUrl(redirectUrl)) {
                    throw new Error(`oauth.clientRegistration "cimd" needs Client ID Metadata Documents that list the redirect URI ${redirectUrl}`);
                }
                const document = clientMetadataDocument(serverUrl, redirectUrl, metadata, documents.baseUrl);
                if (!document && settings.clientRegistration === "cimd") {
                    throw new Error('The authorization server does not support Client ID Metadata Documents for public clients; remove oauth.clientRegistration "cimd"');
                }
                return document;
            }
            : undefined,
        clientId: settings.clientId,
        clientSecret: settings.clientSecret,
        store: options.store,
        onRedirect,
    });
}
/**
 * Whether the client may identify with a Client ID Metadata Document instead of registering. `auto`
 * registers when the user asked for a client name or the documents do not list the redirect URI.
 */
export function offersClientMetadataDocument(settings, documents, redirectUrl) {
    const registration = settings.clientRegistration ?? "auto";
    if (registration !== "auto")
        return registration === "cimd";
    return !settings.clientId && !settings.clientName && documents?.listsRedirectUrl(redirectUrl) === true;
}
/**
 * The Client ID Metadata Document to use, chosen like Codex chooses its own, or `undefined` when the
 * authorization server does not support documents for public clients. Without the `iss` parameter in
 * authorization responses (RFC 9207), the redirect URI and the document are specific to the MCP server,
 * so a response cannot be mixed up with one from another authorization server (RFC 9700 section 4.4.2.2).
 */
function clientMetadataDocument(serverUrl, redirectUrl, metadata, baseUrl) {
    if (!metadata?.client_id_metadata_document_supported ||
        !metadata.token_endpoint_auth_methods_supported?.includes("none")) {
        return undefined;
    }
    if (metadata.authorization_response_iss_parameter_supported)
        return { url: `${baseUrl}/client.json`, redirectUrl };
    const redirect = new URL(redirectUrl);
    redirect.pathname = serverSpecificPath(redirect.pathname, serverUrl);
    return { url: `${baseUrl}/${callbackId(serverUrl)}/client.json`, redirectUrl: redirect.href };
}
/** The path of the redirect URI of a server-specific Client ID Metadata Document: `<path>/<callback ID>`. */
export function serverSpecificPath(path, serverUrl) {
    return `${path.replace(/\/$/, "")}/${callbackId(serverUrl)}`;
}
/** 12 characters identifying an MCP server URL in callback paths, computed like Codex does. */
function callbackId(serverUrl) {
    const url = new URL(serverUrl);
    url.hash = "";
    return createHash("sha256").update(url.href).digest().subarray(0, 9).toString("base64url");
}
/** Redirect URIs a stored client was registered with. */
export function registeredRedirectUrls(client) {
    return client && "redirect_uris" in client ? client.redirect_uris : [];
}
/**
 * Bounds each request to the authorization server, so an unresponsive one cannot hold the refresh lock,
 * delay shutdown, or keep a sign-in waiting for long.
 */
const OAUTH_REQUEST_TIMEOUT_MS = 15_000;
/** `fetch` with `OAUTH_REQUEST_TIMEOUT_MS` per request, on top of the request's own signal. */
export function timedFetch(fetch = globalThis.fetch) {
    return (input, init) => {
        const timeout = AbortSignal.timeout(OAUTH_REQUEST_TIMEOUT_MS);
        return fetch(input, { ...init, signal: init?.signal ? AbortSignal.any([init.signal, timeout]) : timeout });
    };
}
const CALLBACK_HOST = "127.0.0.1";
const CALLBACK_PATH = "/callback";
/** Redirect URI when the settings name none and no client is stored. */
export const DEFAULT_REDIRECT_URL = `http://${CALLBACK_HOST}${CALLBACK_PATH}`;
/** The loopback callback settings of `settings`. */
export function loopbackSettings(settings) {
    const configured = settings.callbackUrl ?? DEFAULT_REDIRECT_URL;
    const url = new URL(configured);
    const address = url.hostname.replace(/^\[|\]$/g, "");
    const port = url.port ? Number(url.port) : settings.callbackPort;
    let fixedRedirectUrl;
    // A configured URI with a port is sent exactly as written, since servers compare it as a string.
    if (url.port)
        fixedRedirectUrl = settings.callbackUrl;
    else if (port !== undefined) {
        url.port = String(port);
        fixedRedirectUrl = url.href;
    }
    return {
        url: configured,
        // `localhost` is served on 127.0.0.1; browsers fall back to it when ::1 refuses.
        host: address === "localhost" ? CALLBACK_HOST : address,
        redirectHost: address,
        port,
        path: url.pathname,
        fixedRedirectUrl,
    };
}
//# sourceMappingURL=client-settings.js.map