export class OAuthError extends Error {
    code;
    errorUri;
    constructor(code, message, errorUri) {
        super(message || code);
        this.name = "OAuthError";
        this.code = code;
        this.errorUri = errorUri;
    }
}
export class OAuthIssuerMismatchError extends Error {
    expected;
    /** `undefined` when an authorization response lacks the `iss` parameter its server promised (RFC 9207). */
    received;
    constructor(expected, received) {
        super(`OAuth issuer mismatch: expected ${JSON.stringify(expected)}, received ${received === undefined ? "none" : JSON.stringify(received)}`);
        this.name = "OAuthIssuerMismatchError";
        this.expected = expected;
        this.received = received;
    }
}
export class OAuthInsecureEndpointError extends Error {
    endpoint;
    constructor(endpoint) {
        super(`Refusing to send OAuth credentials to non-HTTPS endpoint ${endpoint}`);
        this.name = "OAuthInsecureEndpointError";
        this.endpoint = endpoint;
    }
}
export class OAuthRegistrationError extends Error {
    status;
    body;
    constructor(status, body) {
        super(`OAuth dynamic client registration failed with status ${status}: ${body}`);
        this.name = "OAuthRegistrationError";
        this.status = status;
        this.body = body;
    }
}
export class McpOAuthAuthorizationRequiredError extends Error {
    constructor() {
        super("MCP OAuth authorization requires user interaction");
        this.name = "McpOAuthAuthorizationRequiredError";
    }
}
//# sourceMappingURL=errors.js.map