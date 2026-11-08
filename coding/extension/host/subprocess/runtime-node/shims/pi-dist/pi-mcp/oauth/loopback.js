/**
 * How the authorization response of a sign-in reaches a local application: a loopback callback server
 * the browser redirects to, or a redirect URL the user pastes from the browser address bar.
 */
import { OAuthCallbackServer } from "./callback.js";
import { loopbackSettings, offersClientMetadataDocument, registeredRedirectUrls, serverSpecificPath, } from "./client-settings.js";
import { cancellable, McpSignInCancelledError, } from "./sign-in.js";
/**
 * Listen for the authorization response of a sign-in on loopback, where `settings` say: `callbackUrl`
 * and `callbackPort`, else the port of the stored client's redirect URI (so that client stays valid) or
 * a free one. Pass the sign-in's `clientMetadataDocuments`, so the callback server also serves the
 * server-specific redirect URI. Close the callback server when the sign-in ends.
 */
export async function listenForLoopbackSignIn(options) {
    const { settings } = options;
    const loopback = loopbackSettings(settings);
    const registered = registeredRedirectUrls((await options.store.load())?.clientInformation)[0];
    const port = loopback.port ?? (registered ? Number(new URL(registered).port) || undefined : undefined);
    const offersDocument = offersClientMetadataDocument(settings, options.clientMetadataDocuments, loopback.url);
    const listenOptions = {
        host: loopback.host,
        redirectHost: loopback.redirectHost,
        path: loopback.path,
        // The redirect URI of a server-specific Client ID Metadata Document.
        extraPaths: offersDocument ? [serverSpecificPath(loopback.path, options.serverUrl)] : [],
        renderPage: options.renderPage,
    };
    let callback;
    try {
        callback = await OAuthCallbackServer.listen({ ...listenOptions, port: port ?? 0 });
    }
    catch (error) {
        // A configured port is required; the stored client's port is only preferred.
        if (loopback.port !== undefined || port === undefined)
            throw error;
        callback = await OAuthCallbackServer.listen(listenOptions);
    }
    return { callback, redirectUrl: loopback.fixedRedirectUrl ?? callback.redirectUrl };
}
/**
 * Wait for the authorization response on a loopback callback server, or for a redirect URL the user
 * pastes from the browser address bar, whichever comes first. Aborting `signal` stops the wait with
 * `McpSignInCancelledError`.
 */
export async function waitForLoopbackSignIn(options) {
    const { authorization } = options;
    const controller = new AbortController();
    const signal = options.signal ? AbortSignal.any([controller.signal, options.signal]) : controller.signal;
    const fromBrowser = options.callback.waitForCallback(authorization.state, new URL(authorization.redirectUrl).pathname);
    const fromUser = options.promptForRedirectUrl
        ? options.promptForRedirectUrl(signal).then((input) => {
            if (!input?.trim())
                throw new McpSignInCancelledError();
            return responseFromRedirectUrl(input, authorization);
        })
        : new Promise((_, reject) => {
            signal.addEventListener("abort", () => reject(new McpSignInCancelledError()), { once: true });
        });
    try {
        return await cancellable(options.signal, () => Promise.race([fromBrowser, fromUser]));
    }
    finally {
        // The losing side rejects once the prompt is aborted or the callback server closes.
        controller.abort();
        fromBrowser.catch(() => undefined);
        fromUser.catch(() => undefined);
    }
}
/**
 * The authorization response in a redirect URL the user pasted from the browser address bar. Fails when
 * the URL is not on the sign-in's redirect URI, carries another `state`, or reports an error.
 */
export function responseFromRedirectUrl(input, authorization) {
    let url;
    try {
        url = new URL(input.trim());
    }
    catch {
        throw new Error("Expected the full redirect URL from the browser address bar");
    }
    const redirectUrl = new URL(authorization.redirectUrl);
    // A server-specific redirect URI tells authorization servers apart, so it must match exactly.
    if (url.origin !== redirectUrl.origin || url.pathname !== redirectUrl.pathname) {
        throw new Error("The redirect URL does not match this sign-in's redirect URI");
    }
    const error = url.searchParams.get("error");
    if (error)
        throw new Error(url.searchParams.get("error_description") ?? error);
    const state = url.searchParams.get("state");
    if (state !== authorization.state)
        throw new Error("The redirect URL belongs to a different sign-in");
    const code = url.searchParams.get("code");
    if (!code)
        throw new Error("The redirect URL does not contain an authorization code");
    const iss = url.searchParams.get("iss");
    return { code, state, ...(iss ? { iss } : {}) };
}
//# sourceMappingURL=loopback.js.map