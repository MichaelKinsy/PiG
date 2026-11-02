package documents

import "sync/atomic"

// Ports packages/chord/src/next/documents/fence.ts
//
// The seal fence (slice 6a §1.3): the token an attempt, a job run's invocation or the object itself hands its
// transactions. Once sealed, new transactions under it are refused `released` and the open one is discarded.

// SealToken is one seal token; sealed once, never unsealed. It is safe for use from several goroutines: the line's
// waiters read it outside the host's sections.
type SealToken struct{ sealed atomic.Bool }

// NewSealToken is an open token.
func NewSealToken() *SealToken { return &SealToken{} }

// Sealed reports whether work under this token is refused.
func (token *SealToken) Sealed() bool { return token.sealed.Load() }

// Seal seals the token; Documents.Fence also discards the transaction it holds open.
func (token *SealToken) Seal() { token.sealed.Store(true) }
