package host

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/host/revocation.test.ts for the part that needs no host: `revocation keys`. Upstream runs
// its two cases through createHost (host.revoke, a RawPeer of hold messages), which belongs to the host core; these are own
// tests of revocationKeys with the behavior each of them asserts: the keys are computed once per principal object and
// compared as canonical JSON ("are computed once per principal object, compared as canonical JSON"), and the cache keeps
// no principal alive ("keep no principal alive (a WeakMap keyed by the principal the host cached them for)"). The
// remaining cases (revoke on one host, links, GC of revoked objects, relays, own requests over paths) are the host's.

func revocationGuest(session string) data.Principal {
	return delta.JsonObjectOf("kind", "guest", "session", session)
}

func revocationCanonical(t *testing.T, value delta.JsonValue) string {
	t.Helper()
	return data.CanonicalJson(value, chordctx.Background())
}

// Own test mirroring "are computed once per principal object, compared as canonical JSON": two principals revoked twice
// each call revokedBy twice in all, and a revocation spelled with another key order is the same key.
func TestRevocationKeysAreComputedOncePerPrincipalObjectComparedAsCanonicalJSON(t *testing.T) {
	calls := 0
	keys := newRevocationKeys(RevokedBy[delta.JsonValue](func(data.Principal) ([]delta.JsonValue, error) {
		calls++
		return []delta.JsonValue{delta.JsonObjectOf("b", 2.0, "a", 1.0)}, nil
	}).erased())
	first, second := revocationGuest("S1"), revocationGuest("S2")
	ctx := chordctx.Background()
	match := revocationCanonical(t, delta.JsonObjectOf("a", 1.0, "b", 2.0))
	miss := revocationCanonical(t, delta.JsonObjectOf("a", 1.0, "b", 3.0))
	for _, principal := range []data.Principal{first, second} {
		if revoked, err := keys.revokes(principal, match, ctx); err != nil || !revoked {
			t.Fatalf("revokes(%v, match) = %v, %v; want true, nil", principal, revoked, err)
		}
		if revoked, err := keys.revokes(principal, miss, ctx); err != nil || revoked {
			t.Fatalf("revokes(%v, miss) = %v, %v; want false, nil", principal, revoked, err)
		}
	}
	if calls != 2 {
		t.Fatalf("revokedBy calls = %d, want 2", calls)
	}
}

// Own test mirroring "keep no principal alive": the cache holds a principal weakly, and goes on working for the others.
func TestRevocationKeysKeepNoPrincipalAlive(t *testing.T) {
	keys := newRevocationKeys(RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
		return []delta.JsonValue{delta.JsonObjectOf("session", principal.Value("session"))}, nil
	}).erased())
	key := revocationCanonical(t, delta.JsonObjectOf("session", "S9"))
	cached := func() weak.Pointer[delta.JsonObject] {
		principal := revocationGuest("S9")
		if revoked, err := keys.revokes(principal, key, chordctx.Background()); err != nil || !revoked {
			t.Fatalf("revokes = %v, %v; want true, nil", revoked, err)
		}
		return weak.Make(principal)
	}()
	nexttest.ExpectCollected(t, cached)
	// The cache's owner is still alive and working.
	other := revocationGuest("S9")
	if revoked, err := keys.revokes(other, key, chordctx.Background()); err != nil || !revoked {
		t.Fatalf("revokes after the collection = %v, %v; want true, nil", revoked, err)
	}
	// Its entry went with the principal: only the live one is left.
	var entries int
	for range 10 {
		runtime.GC()
		keys.keys.mu.Lock()
		entries = len(keys.keys.entries)
		keys.keys.mu.Unlock()
		if entries == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if entries != 1 {
		t.Fatalf("cached principals = %d, want 1", entries)
	}
	runtime.KeepAlive(other)
}

// Own test of what Pi's `revokes` does when `revokedBy` throws: the throw reaches the caller and nothing is cached, so the
// next call asks `revokedBy` again.
func TestRevocationKeysReturnTheErrorOfRevokedByAndCacheNothing(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	keys := newRevocationKeys(func(data.Principal) ([]delta.JsonValue, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return []delta.JsonValue{"x"}, nil
	})
	principal := revocationGuest("S1")
	if _, err := keys.revokes(principal, `"x"`, chordctx.Background()); !errors.Is(err, boom) {
		t.Fatalf("first revokes error = %v, want boom", err)
	}
	if revoked, err := keys.revokes(principal, `"x"`, chordctx.Background()); err != nil || !revoked {
		t.Fatalf("second revokes = %v, %v; want true, nil", revoked, err)
	}
	if calls != 2 {
		t.Fatalf("revokedBy calls = %d, want 2", calls)
	}
}

// Own test of `revokedBy === undefined`: nothing revokes, and an absent function stays absent through the host's cast.
func TestRevocationKeysWithoutRevokedByRevokeNothing(t *testing.T) {
	var absent RevokedBy[delta.JsonValue]
	if absent.erased() != nil {
		t.Fatal("an absent revokedBy erased is not nil")
	}
	keys := newRevocationKeys(absent.erased())
	if revoked, err := keys.revokes(revocationGuest("S1"), `"x"`, chordctx.Background()); err != nil || revoked {
		t.Fatalf("revokes = %v, %v; want false, nil", revoked, err)
	}
}

// Own test of the cast: the typed revocations become JSON values in order.
func TestRevokedByErasedKeepsTheOrderOfTheRevocations(t *testing.T) {
	erased := RevokedBy[string](func(data.Principal) ([]string, error) { return []string{"a", "b"}, nil }).erased()
	values, err := erased(revocationGuest("S1"))
	if err != nil || !reflect.DeepEqual(values, []delta.JsonValue{"a", "b"}) {
		t.Fatalf("erased = %v, %v; want [a b], nil", values, err)
	}
}
