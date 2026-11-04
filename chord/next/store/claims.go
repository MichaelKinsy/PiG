package store

import (
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/claims.ts
//
// Claims (slice 6b "owner names and claims", "claims per table and per document namespace"): what can collide in a
// store is claimed once, in memory: a table by its SQL name, documents by `(owner, namespace)`. A claim is held by
// its claimant (an object, a `StoreStorage`) until its end releases every claim it holds. While a batch applies,
// nothing is claimed: batch apply and owners exclude each other both ways.
//
// Go forced difference: Pi's claimant is any object with a `label`; Go's is a Claimant, compared by identity (use
// pointers). A mutex guards the maps, as owners may declare from several goroutines.

// Claimant is who holds claims: named in the refusal of a second claim. Claimants are compared by identity: use a
// pointer type (NamedClaimant is the plain one).
type Claimant interface{ ClaimLabel() string }

// NamedClaimant is a Claimant that is only a label (Pi's `{ label }`).
type NamedClaimant struct{ Label string }

// ClaimLabel is the label.
func (claimant *NamedClaimant) ClaimLabel() string { return claimant.Label }

// Claims are a store's claims, by key; one claimant per key.
type Claims struct {
	mu      sync.Mutex
	holders map[string]Claimant
	keys    map[Claimant][]string
	// locked: whether a batch applies now: no claim is taken meanwhile.
	locked bool
}

// NewClaims is an empty set of claims.
func NewClaims() *Claims {
	return &Claims{holders: map[string]Claimant{}, keys: map[Claimant][]string{}}
}

// Held reports whether any claimant holds a claim (batch apply is refused then: its memory would go stale).
func (claims *Claims) Held() bool {
	claims.mu.Lock()
	defer claims.mu.Unlock()
	return len(claims.keys) > 0
}

// Table claims table (its SQL name) for claimant; held by another: invalid/args.
func (claims *Claims) Table(table string, claimant Claimant) error {
	text, err := jsstring.MarshalJSON(table)
	if err != nil {
		return err
	}
	return claims.claim("table "+string(text), claimant)
}

// AssertTable fails as Table would for claimant, claiming nothing (a reload's staging checks its declarations).
func (claims *Claims) AssertTable(table string, claimant Claimant) error {
	text, err := jsstring.MarshalJSON(table)
	if err != nil {
		return err
	}
	return claims.assert("table "+string(text), claimant)
}

// Documents claims the documents of owner in namespace for claimant; held by another: invalid/args.
func (claims *Claims) Documents(owner, namespace string, claimant Claimant) error {
	text, err := jsstring.MarshalJSON([]string{owner, namespace})
	if err != nil {
		return err
	}
	return claims.claim("documents "+string(text), claimant)
}

// AssertDocuments fails as Documents would for claimant, claiming nothing.
func (claims *Claims) AssertDocuments(owner, namespace string, claimant Claimant) error {
	text, err := jsstring.MarshalJSON([]string{owner, namespace})
	if err != nil {
		return err
	}
	return claims.assert("documents "+string(text), claimant)
}

// Lock refuses every claim (invalid/args) until the returned unlock: a batch applies meanwhile.
func (claims *Claims) Lock() (unlock func()) {
	claims.mu.Lock()
	claims.locked = true
	claims.mu.Unlock()
	return func() {
		claims.mu.Lock()
		claims.locked = false
		claims.mu.Unlock()
	}
}

// Release releases every claim claimant holds.
func (claims *Claims) Release(claimant Claimant) {
	claims.mu.Lock()
	defer claims.mu.Unlock()
	for _, key := range claims.keys[claimant] {
		delete(claims.holders, key)
	}
	delete(claims.keys, claimant)
}

func (claims *Claims) claim(key string, claimant Claimant) error {
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if err := claims.refusal(key, claimant); err != nil {
		return err
	}
	if claims.holders[key] == claimant {
		return nil
	}
	claims.holders[key] = claimant
	claims.keys[claimant] = append(claims.keys[claimant], key)
	return nil
}

// assert refuses key to claimant as claim does, taking nothing.
func (claims *Claims) assert(key string, claimant Claimant) error {
	claims.mu.Lock()
	defer claims.mu.Unlock()
	return claims.refusal(key, claimant)
}

// refusal is why key is refused to claimant while a batch applies or another claimant holds it; nil when it is not. The
// caller holds mu.
func (claims *Claims) refusal(key string, claimant Claimant) error {
	if claims.locked {
		return data.Invalid(data.ReasonArgs, "store: "+key+" cannot be claimed while a batch applies")
	}
	if holder, held := claims.holders[key]; held && holder != claimant {
		return data.Invalid(data.ReasonArgs, "store: "+key+" is claimed by "+holder.ClaimLabel())
	}
	return nil
}
