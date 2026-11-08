package data

import "context"

// Ports packages/chord/src/data/principal.ts `samePrincipal` (Pi main c23b9ec1).

// SamePrincipal reports whether principal is other: the same default key (canonical JSON), each computed once per
// principal object; no principal (nil, upstream's `undefined`) is none.
func SamePrincipal(principal, other Principal, ctx context.Context) bool {
	return principal != nil && PrincipalKey(principal, ctx, nil) == PrincipalKey(other, ctx, nil)
}
