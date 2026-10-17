// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// addressID is documents.ts addressId: JSON.stringify([kind, scope kind, owner or null, key or null]). It is built in place; the
// text it makes is the document's map key and is compared across steps, so it must equal what the stringify of the array makes.
func TestAddressIDIsTheStringifyOfItsArray(t *testing.T) {
	keys := []string{"", "k", "a\"b", "\\", "line\nfeed", "é", "\u2028", "\xed\xa0\x80", "0", "12"}
	kinds := []string{"", "note", "a\"b", "é"}
	scopes := []Scope{{Kind: ScopeSession}, {Kind: ScopeConversation, Owner: 7}, {Kind: ScopeTask, Owner: 1 << 40}, {Kind: ScopeConversation, Owner: 0}}
	for _, kind := range kinds {
		for _, sc := range scopes {
			for _, family := range []bool{false, true} {
				for _, key := range keys {
					a := Address{Kind: kind, Scope: sc, Key: key, Family: family}
					var owner, k any
					if sc.Kind != ScopeSession {
						owner = idv(sc.Owner)
					}
					if family {
						k = key
					}
					if got, want := addressID(a), jv.Stringify([]any{kind, string(sc.Kind), owner, k}); got != want {
						t.Fatalf("%+v: %s, want %s", a, got, want)
					}
				}
			}
		}
	}
}
