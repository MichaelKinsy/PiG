/**
 * Sign-in to an OAuth-protected MCP server, in two halves so an application can complete it in another
 * request with the same state store: `startMcpSignIn` refreshes the stored tokens when it can, or else
 * prepares the browser step and returns the authorization URL; `completeMcpSignIn` exchanges the
 * authorization code. The code arrives through a loopback callback server (`loopback.ts`), the
 * application's own https callback route, or a redirect URL the user pastes.
 */
import { refreshStoredTokens } from "./auth-provider.js";
import { createOAuthClientProvider, offersClientMetadataDocument, registeredRedirectUrls, timedFetch, } from "./client-settings.js";
import { McpRefreshLockBusyError } from "./errors.js";
import { authorizeMcp, stepUpScope } from "./flow.js";
/** The user or the application cancelled a sign-in (its `signal` was aborted, or the user gave up). */
export class McpSignInCancelledError extends Error {
    constructor() {
        super("Sign-in cancelled");
        this.name = "McpSignInCancelledError";
    }
}
/**
 * First half of a sign-in. Without a step-up (`insufficient_scope`), it first tries to refresh the stored
 * tokens with the stored client, whatever `redirectUrl` is (`authorized`). The refresh takes the store's
 * refresh lock, so never call this while holding it; when another process holds the lock for the whole
 * wait (`McpRefreshLockBusyError`), the refresh is skipped, and any other failure of the lock fails the
 * sign-in. Otherwise it prepares the browser step (`redirect`): a fresh `state`, and a stored client that
 * cannot use `redirectUrl` is dropped with its tokens. Requests to the authorization server are
 * time-limited.
 */
export async function startMcpSignIn(options) {
    if (options.signal?.aborted)
        throw new McpSignInCancelledError();
    // A refresh keeps the granted scope; a server asking for more needs the browser step.
    const stepUp = options.challenge?.error === "insufficient_scope";
    if (!stepUp && (await refreshBeforeSignIn(options)) === "AUTHORIZED")
        return { kind: "authorized" };
    return cancellable(options.signal, () => startBrowserStep(options, stepUp));
}
/**
 * Refresh the stored tokens, unless `cimd` is to replace a registered client. The refresh uses
 * `redirectUrl` when the tokens came from a Client ID Metadata Document whose documents list it.
 */
async function refreshBeforeSignIn(options) {
    const { store, settings, challenge, redirectUrl, signal } = options;
    const refreshed = store.withRefreshLock(async () => {
        // The lock may arrive after the sign-in was cancelled: return at once, so it is released.
        if (signal?.aborted)
            return "REDIRECT";
        const state = await store.load();
        if (!state?.tokens?.refresh_token)
            return "REDIRECT";
        if (settings.clientRegistration === "cimd" && state.clientInformation && !settings.clientId)
            return "REDIRECT";
        return refreshStoredTokens(options, state, { challenge, redirectUrl });
    });
    try {
        return await untilAborted(signal, refreshed);
    }
    catch (error) {
        // Another process is refreshing and took too long. The user signs in instead.
        if (error instanceof McpRefreshLockBusyError)
            return "REDIRECT";
        throw error;
    }
}
/**
 * `promise`, or `McpSignInCancelledError` as soon as `signal` aborts. `promise` keeps running: a refresh
 * the server answered must save its tokens.
 */
function untilAborted(signal, promise) {
    if (!signal)
        return promise;
    return new Promise((resolve, reject) => {
        const abort = () => reject(new McpSignInCancelledError());
        signal.addEventListener("abort", abort, { once: true });
        promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
    });
}
async function startBrowserStep(options, stepUp) {
    const { store, settings, redirectUrl, challenge } = options;
    const stored = await store.load();
    if (stored)
        await store.save(stateForSignIn(stored, options));
    let authorizationUrl;
    const provider = createOAuthClientProvider(options, redirectUrl, (url) => {
        authorizationUrl = url;
    });
    // A server asking for more scope gets it on top of the configured scope and, since the challenge
    // may list only the missing scopes, on top of the scope granted so far.
    const scope = mergeScopes(settings.scope, stepUp ? stepUpScope(stored?.tokens?.scope, challenge?.scope) : challenge?.scope);
    await authorizeMcp(provider, { ...flowOptions(options), scope, skipRefresh: true });
    if (!authorizationUrl)
        throw new Error("OAuth flow did not produce an authorization URL");
    const requestedScope = authorizationUrl.searchParams.get("scope");
    return {
        kind: "redirect",
        authorizationUrl: authorizationUrl.href,
        // The flow picks the redirect URI, which may be specific to the MCP server.
        redirectUrl: authorizationUrl.searchParams.get("redirect_uri") ?? redirectUrl,
        state: await provider.state(),
        ...(requestedScope ? { scope: requestedScope } : {}),
    };
}
/**
 * The stored state for a browser step: without the last sign-in's `state`, and without a client that
 * cannot use `redirectUrl`, along with its tokens.
 */
function stateForSignIn(stored, options) {
    const { settings, redirectUrl } = options;
    const next = { ...stored };
    delete next.oauthState;
    // A registered client cannot use another redirect URI, and its tokens belong to it. `cimd` replaces a
    // registered client; `auto` keeps it. A Client ID Metadata Document is not stored, so tokens without
    // a stored client belong to the document.
    const keepClient = settings.clientId ||
        (stored.clientInformation
            ? settings.clientRegistration !== "cimd" &&
                registeredRedirectUrls(stored.clientInformation).includes(redirectUrl)
            : offersClientMetadataDocument(settings, options.clientMetadataDocuments, redirectUrl));
    if (!keepClient) {
        delete next.clientInformation;
        delete next.tokens;
        delete next.tokensExpireAt;
    }
    return next;
}
/** Scopes of both lists, each once. */
function mergeScopes(...scopes) {
    const merged = [...new Set(scopes.flatMap((scope) => scope?.split(/\s+/).filter(Boolean) ?? []))];
    return merged.length > 0 ? merged.join(" ") : undefined;
}
/**
 * Second half of a sign-in: exchanges the authorization code of `response` and saves the tokens. Pass
 * the options given to `startMcpSignIn`, including its `redirectUrl` (not `authorization.redirectUrl`).
 * Fails when the response carries another `state`, when another sign-in started with the store since, or
 * when the exchange would use another redirect URI than the authorization request (the options changed).
 * A redirect URL the user pasted goes through `responseFromRedirectUrl` first, which checks its path.
 */
export async function completeMcpSignIn(options) {
    await cancellable(options.signal, async () => {
        const { authorization, response } = options;
        if (response.state !== authorization.state) {
            throw new Error("The authorization response belongs to a different sign-in");
        }
        // The stored PKCE verifier belongs to the latest sign-in; exchanging with it would fail with
        // `invalid_grant`, which discards the stored tokens.
        if ((await options.store.load())?.oauthState !== authorization.state) {
            throw new Error("Another sign-in to this MCP server started since; sign in again");
        }
        await authorizeMcp(createOAuthClientProvider(options, options.redirectUrl), {
            ...flowOptions(options),
            // Saved with the tokens when the token response names no scope, so it must be the requested one.
            scope: authorization.scope,
            authorizationRedirectUrl: authorization.redirectUrl,
            authorizationCode: response.code,
            iss: response.iss,
        });
    });
}
/** `authorizeMcp` options of a sign-in, without the scope. */
function flowOptions(options) {
    return {
        serverUrl: options.serverUrl,
        fetch: timedFetch(),
        signal: options.signal,
        resourceMetadataUrl: options.challenge?.resourceMetadataUrl,
        authorizationServerMetadataUrl: options.settings.authServerMetadataUrl,
    };
}
/** Run `fn`, reporting an abort of `signal` as `McpSignInCancelledError`. */
export async function cancellable(signal, fn) {
    if (signal?.aborted)
        throw new McpSignInCancelledError();
    try {
        return await fn();
    }
    catch (error) {
        // Aborted requests fail with the signal's reason; report them as the cancellation they are.
        if (signal?.aborted)
            throw new McpSignInCancelledError();
        throw error;
    }
}
//# sourceMappingURL=sign-in.js.map