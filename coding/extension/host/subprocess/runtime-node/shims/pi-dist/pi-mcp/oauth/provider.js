export class MemoryOAuthStateStore {
    value;
    load() {
        return this.value === undefined ? undefined : structuredClone(this.value);
    }
    save(state) {
        this.value = structuredClone(state);
    }
}
/** Default stateful provider for one exact MCP server URL. Applications inject durable storage if needed. */
export class McpOAuthProvider {
    redirectUrl;
    clientMetadata;
    serverUrl;
    configuredClient;
    store;
    onRedirect;
    writes = Promise.resolve();
    constructor(options) {
        this.serverUrl = String(new URL(options.serverUrl));
        this.redirectUrl = String(options.redirectUrl);
        this.clientMetadata = {
            ...options.clientMetadata,
            redirect_uris: options.clientMetadata.redirect_uris ?? [this.redirectUrl],
            grant_types: options.clientMetadata.grant_types ?? ["authorization_code", "refresh_token"],
            response_types: options.clientMetadata.response_types ?? ["code"],
            token_endpoint_auth_method: options.clientMetadata.token_endpoint_auth_method ?? (options.clientSecret ? "client_secret_post" : "none"),
        };
        this.configuredClient = options.clientId
            ? { client_id: options.clientId, ...(options.clientSecret ? { client_secret: options.clientSecret } : {}) }
            : undefined;
        this.store = options.store ?? new MemoryOAuthStateStore();
        this.onRedirect = options.onRedirect;
    }
    async state() {
        const existing = (await this.load()).oauthState;
        if (existing)
            return existing;
        const state = Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString("hex");
        await this.update((value) => ({ ...value, oauthState: state }));
        return state;
    }
    async clientInformation() {
        return this.configuredClient ?? (await this.load()).clientInformation;
    }
    async saveClientInformation(information) {
        if (this.configuredClient)
            return;
        await this.update((value) => ({ ...value, clientInformation: information }));
    }
    async tokens() {
        return (await this.load()).tokens;
    }
    async saveTokens(tokens) {
        const expiresAt = tokens.expires_in === undefined ? undefined : Date.now() + tokens.expires_in * 1000;
        await this.update((value) => {
            const next = { ...value, tokens };
            if (expiresAt === undefined)
                delete next.tokensExpireAt;
            else
                next.tokensExpireAt = expiresAt;
            return next;
        });
    }
    async redirectToAuthorization(url) {
        await this.onRedirect(url);
    }
    async saveCodeVerifier(verifier) {
        await this.update((value) => ({ ...value, codeVerifier: verifier }));
    }
    async codeVerifier() {
        const verifier = (await this.load()).codeVerifier;
        if (!verifier)
            throw new Error("No OAuth PKCE code verifier is stored");
        return verifier;
    }
    async invalidateCredentials(kind) {
        await this.update((value) => {
            const next = { ...value };
            if (kind === "all" || kind === "client")
                delete next.clientInformation;
            if (kind === "all" || kind === "tokens") {
                delete next.tokens;
                delete next.tokensExpireAt;
            }
            if (kind === "all" || kind === "verifier")
                delete next.codeVerifier;
            if (kind === "all" || kind === "discovery")
                delete next.discovery;
            if (kind === "all")
                delete next.oauthState;
            return next;
        });
    }
    async saveDiscoveryState(discovery) {
        await this.update((value) => ({ ...value, discovery }));
    }
    async discoveryState() {
        return (await this.load()).discovery;
    }
    async load() {
        await this.writes;
        return this.own(await this.store.load());
    }
    async update(update) {
        this.writes = this.writes.then(async () => {
            await this.store.save(update(this.own(await this.store.load())));
        });
        await this.writes;
    }
    /** Stored state for another server URL is ignored so credentials never leak across servers. */
    own(state) {
        return state?.serverUrl === this.serverUrl ? state : { serverUrl: this.serverUrl };
    }
}
//# sourceMappingURL=provider.js.map