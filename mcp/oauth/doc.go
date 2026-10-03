// Package oauth is the MCP OAuth client subset: protected-resource and
// authorization-server discovery, the PKCE authorization code flow, dynamic
// client registration, token refresh (one refresh shared by concurrent 401s),
// and step-up authorization for insufficient_scope. It does not open a
// browser or choose where credentials are stored.
//
// The implementation is adapted from the MIT-licensed Model Context Protocol
// TypeScript SDK v1.29.0, as upstream's is. Its license is in
// LICENSES/modelcontextprotocol-typescript-sdk.txt.
package oauth
