import { createServer } from "node:http";
function plainText(page) {
    if (page.ok)
        return "Authorization complete. You may close this window.";
    return page.details ? `${page.message}\n\n${page.details}` : page.message;
}
export class OAuthCallbackServer {
    redirectUrl;
    server;
    paths;
    timeoutMs;
    renderPage;
    pending = new Map();
    constructor(server, redirectUrl, paths, timeoutMs, renderPage) {
        this.server = server;
        this.redirectUrl = redirectUrl;
        this.paths = paths;
        this.timeoutMs = timeoutMs;
        this.renderPage = renderPage;
    }
    static async listen(options = {}) {
        const host = options.host ?? "127.0.0.1";
        const redirectHost = options.redirectHost ?? host;
        const path = options.path ?? "/callback";
        let instance;
        const server = createServer((request, response) => instance?.handle(request.url ?? "/", response));
        await new Promise((resolve, reject) => {
            server.once("error", reject);
            server.listen(options.port ?? 0, host, () => {
                server.off("error", reject);
                resolve();
            });
        });
        const address = server.address();
        if (!address || typeof address === "string")
            throw new Error("OAuth callback server did not bind to TCP");
        instance = new OAuthCallbackServer(server, `http://${redirectHost.includes(":") ? `[${redirectHost}]` : redirectHost}:${address.port}${path}`, [path, ...(options.extraPaths ?? [])], options.timeoutMs ?? 5 * 60_000, options.renderPage);
        return instance;
    }
    /**
     * Wait for the authorization response with `state`. With `path`, a response on another path fails, so
     * a server-specific redirect URI can tell authorization servers apart (RFC 9700 section 4.4.2.2).
     */
    waitForCallback(state, path) {
        if (this.pending.has(state))
            throw new Error("OAuth state is already pending");
        return new Promise((resolve, reject) => {
            const timer = setTimeout(() => {
                this.pending.delete(state);
                reject(new Error("OAuth callback timed out"));
            }, this.timeoutMs);
            this.pending.set(state, { resolve, reject, timer, path });
        });
    }
    async close() {
        for (const pending of this.pending.values()) {
            clearTimeout(pending.timer);
            pending.reject(new Error("OAuth callback server closed"));
        }
        this.pending.clear();
        await new Promise((resolve, reject) => {
            this.server.close((error) => (error ? reject(error) : resolve()));
        });
    }
    reply(response, status, page) {
        if (this.renderPage) {
            response.writeHead(status, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
            response.end(this.renderPage(page));
        }
        else {
            response.writeHead(status, { "content-type": "text/plain; charset=utf-8" }).end(plainText(page));
        }
    }
    handle(rawUrl, response) {
        const url = new URL(rawUrl, this.redirectUrl);
        if (!this.paths.includes(url.pathname)) {
            this.reply(response, 404, { ok: false, message: "Not found" });
            return;
        }
        const state = url.searchParams.get("state");
        const pending = state ? this.pending.get(state) : undefined;
        if (!state || !pending) {
            this.reply(response, 400, { ok: false, message: "Invalid or expired OAuth state" });
            return;
        }
        clearTimeout(pending.timer);
        this.pending.delete(state);
        if (pending.path !== undefined && url.pathname !== pending.path) {
            pending.reject(new Error("The authorization response arrived on another redirect URI"));
            this.reply(response, 400, { ok: false, message: "Unexpected redirect URI" });
            return;
        }
        const error = url.searchParams.get("error");
        if (error) {
            const description = url.searchParams.get("error_description") ?? error;
            pending.reject(new Error(description));
            this.reply(response, 200, {
                ok: false,
                message: "Authorization failed. You may close this window.",
                details: description,
            });
            return;
        }
        const code = url.searchParams.get("code");
        if (!code) {
            pending.reject(new Error("OAuth callback did not include an authorization code"));
            this.reply(response, 400, { ok: false, message: "Missing authorization code" });
            return;
        }
        const iss = url.searchParams.get("iss");
        pending.resolve({ code, state, ...(iss ? { iss } : {}) });
        this.reply(response, 200, { ok: true });
    }
}
//# sourceMappingURL=callback.js.map