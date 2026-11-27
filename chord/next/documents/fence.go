package documents

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports packages/chord/src/documents/fence.ts
//
// The seal fence (slice 6a §1.3): the token an attempt, a job run's invocation or the object itself hands its
// transactions. Once sealed, new transactions under it are refused `released` and the open one is discarded. It also
// remembers the contexts its owner aborted to end its own work: what they cancelled is not reported (ERNDL-1190).

// SealToken is one seal token; sealed once, never unsealed. It is safe for use from several goroutines: the line's
// waiters read it outside the host's sections.
type SealToken struct {
	sealed atomic.Bool
	mu     sync.Mutex
	// ends are the contexts its owner aborts to end its own work (an attempt's openers and calls, its routines, its job
	// runs cut off), made by the first: a context whose abort came from one of them was ended by the owner's end.
	ends []context.Context
}

// NewSealToken is an open token.
func NewSealToken() *SealToken { return &SealToken{} }

// Sealed reports whether work under this token is refused.
func (token *SealToken) Sealed() bool { return token.sealed.Load() }

// Seal seals the token; Documents.Fence also discards the transaction it holds open.
func (token *SealToken) Seal() { token.sealed.Store(true) }

// EndsWith remembers that its owner aborts ctx to end its own work; called before the abort.
func (token *SealToken) EndsWith(ctx context.Context) {
	if ctx == nil {
		return
	}
	token.mu.Lock()
	token.ends = append(token.ends, ctx)
	token.mu.Unlock()
}

// EndedBy reports whether ctx's abort came from a context its owner aborted to end its own work (ERNDL-1190).
// In Go, chordctx.AbortOrder numbers every WithCancel/WithAbortSignalCancel abort strictly in order; comparing the
// earliest abort order of ctx against the abort order of each remembered own-end context identifies whether one of
// those own-end contexts was the earliest (originating) abort of ctx.
type contextUnwrapper interface {
	UnwrapContext() context.Context
}

func unwrapCtx(ctx context.Context) context.Context {
	for {
		u, ok := ctx.(contextUnwrapper)
		if !ok {
			return ctx
		}
		next := u.UnwrapContext()
		if next == nil || next == ctx {
			return ctx
		}
		ctx = next
	}
}

func (token *SealToken) EndedBy(ctx context.Context) bool {
	order := chordctx.AbortOrder(unwrapCtx(ctx))
	if order == 0 {
		return false
	}
	token.mu.Lock()
	ends := slices.Clone(token.ends)
	token.mu.Unlock()
	for _, end := range ends {
		if endOrder := chordctx.AbortOrder(end); endOrder != 0 && endOrder == order {
			return true
		}
	}
	return false
}
