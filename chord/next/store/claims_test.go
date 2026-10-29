package store

import (
	"strings"
	"testing"
)

// Ports packages/chord/test/next/store/claims.test.ts: tests of claims (slice 6b "owner names and claims", "claims
// per table and per document namespace"): a table by its SQL name and documents by owner and namespace, once per
// store; released by their claimant's end; none taken while a batch applies.
//
// Pending: the third case ("an object claims its tables and documents when it declares them, and releases them at its
// end") runs a Chord host (`loaded`, `host.serve`, `self.state`, `self.table`), which is outside this package; it is
// ported with the host layer.

func claimFails(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
	_ = rejected(t, err, "invalid", "args")
}

func claimed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
}

func TestClaims(t *testing.T) {
	t.Run("refuse a second claimant of a table or of an owner's namespace; one claimant may claim again", func(t *testing.T) {
		claims := NewClaims()
		a := &NamedClaimant{Label: "A"}
		b := &NamedClaimant{Label: "B"}
		claimed(t, claims.Table("x:edits", a))
		claimed(t, claims.Table("x:edits", a))
		claimed(t, claims.Documents("x", "chord", a))
		claimFails(t, claims.Table("x:edits", b), "claimed by A")
		claimFails(t, claims.Documents("x", "chord", b), "claimed by A")
		// Another namespace of the same owner, another table: no collision.
		claimed(t, claims.Documents("x", "session", b))
		claimed(t, claims.Table("x:entries", b))
		if !claims.Held() {
			t.Fatal("Held = false, want true")
		}
		claims.Release(a)
		claimed(t, claims.Table("x:edits", b))
		claims.Release(b)
		if claims.Held() {
			t.Fatal("Held = true, want false")
		}
	})

	t.Run("are refused while locked (a batch applies), taken again once unlocked", func(t *testing.T) {
		claims := NewClaims()
		owner := &NamedClaimant{Label: "A"}
		unlock := claims.Lock()
		claimFails(t, claims.Table("x:edits", owner), "while a batch applies")
		claimFails(t, claims.Documents("x", "chord", owner), "while a batch applies")
		if claims.Held() {
			t.Fatal("Held = true while locked and unclaimed")
		}
		unlock()
		claimed(t, claims.Table("x:edits", owner))
		if !claims.Held() {
			t.Fatal("Held = false, want true")
		}
	})
}
