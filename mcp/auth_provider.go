package mcp

import (
	"context"
	"net/http"
	"net/url"
)

// Ports packages/mcp/src/auth-provider.ts.

// McpFetch performs one HTTP request. *http.Client implements it. Injecting
// one lets a host add proxying or custom networking.
type McpFetch interface {
	Do(request *http.Request) (*http.Response, error)
}

// UnauthorizedContext describes a rejected request.
type UnauthorizedContext struct {
	// Response is the 401 response, or a 403 response whose challenge reports
	// insufficient_scope.
	Response  *http.Response
	ServerURL *url.URL
	Fetch     McpFetch
	// Token is the access token the rejected request carried, if any. A
	// different current token means another request already refreshed it.
	Token string
}

// AuthProvider supplies bearer tokens to an MCP HTTP transport.
type AuthProvider interface {
	// Token returns the current access token, or "" for none.
	Token(ctx context.Context) (string, error)
}

// UnauthorizedHandler is implemented by an [AuthProvider] that can refresh
// credentials after a 401 response. The transport retries the request once
// with whatever credentials the handler left behind.
type UnauthorizedHandler interface {
	OnUnauthorized(ctx context.Context, unauthorized UnauthorizedContext) error
}
