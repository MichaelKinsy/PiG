package host

import (
	"context"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/host/revocation.ts. Revocation keys (§4, R10, R11): what `revokedBy` says revokes a
// principal, as canonical JSON, computed once per principal object.

// RevokedBy is what revokes a principal: the app's typed values, the same function on every host. A returned error is
// Pi's throw.
//
// Go forced difference: a principal is always a data.Principal (a *delta.JsonObject), so only the revocation type R is
// generic where Pi's has the principal's type too.
type RevokedBy[R any] func(principal data.Principal) ([]R, error)

// erased is revokedBy as the host's revocation keys take it: its values as JSON (Pi's cast to
// `RevokedBy<Principal, JsonValue>`); nil stays nil.
func (revokedBy RevokedBy[R]) erased() func(data.Principal) ([]delta.JsonValue, error) {
	if revokedBy == nil {
		return nil
	}
	return func(principal data.Principal) ([]delta.JsonValue, error) {
		revocations, err := revokedBy(principal)
		if err != nil {
			return nil, err
		}
		values := make([]delta.JsonValue, len(revocations))
		for i, revocation := range revocations {
			values[i] = revocation
		}
		return values, nil
	}
}

// revocationKeys is a host's revocation keys: each principal's, cached weakly by the principal object.
type revocationKeys struct {
	revokedBy func(data.Principal) ([]delta.JsonValue, error)
	keys      *principalRevocations
}

// principalRevocations is the cache of a host's keys by principal. It does not retain its principals: an entry is keyed
// by weak pointer and removed once its principal is collected, which happens on a goroutine of its own, so the map is
// locked.
type principalRevocations struct {
	mu      sync.Mutex
	entries map[weak.Pointer[delta.JsonObject]]map[string]struct{}
}

// newRevocationKeys makes the keys of the hosts whose `revokedBy` is revokedBy (nil: nothing revokes).
func newRevocationKeys(revokedBy func(data.Principal) ([]delta.JsonValue, error)) *revocationKeys {
	return &revocationKeys{revokedBy: revokedBy, keys: &principalRevocations{entries: map[weak.Pointer[delta.JsonObject]]map[string]struct{}{}}}
}

// revokes reports whether key (a revocation's canonical JSON) is one of principal's revocation keys: the canonical JSON of
// each value `revokedBy` returns for it, computed on its first use. An error of `revokedBy` is returned and nothing is
// cached.
func (r *revocationKeys) revokes(principal data.Principal, key string, ctx context.Context) (bool, error) {
	revokedBy := r.revokedBy
	if revokedBy == nil {
		return false, nil
	}
	ref := weak.Make(principal)
	cache := r.keys
	cache.mu.Lock()
	keys, ok := cache.entries[ref]
	cache.mu.Unlock()
	if !ok {
		revocations, err := revokedBy(principal)
		if err != nil {
			return false, err
		}
		keys = make(map[string]struct{}, len(revocations))
		for _, revocation := range revocations {
			keys[data.CanonicalJson(revocation, ctx)] = struct{}{}
		}
		cache.mu.Lock()
		cache.entries[ref] = keys
		cache.mu.Unlock()
		// The cleanup holds the cache weakly: a host's keys are collectable while a principal lives on.
		owner := weak.Make(cache)
		runtime.AddCleanup(principal, func(ref weak.Pointer[delta.JsonObject]) {
			if live := owner.Value(); live != nil {
				live.mu.Lock()
				delete(live.entries, ref)
				live.mu.Unlock()
			}
		}, ref)
	}
	_, revoked := keys[key]
	return revoked, nil
}
