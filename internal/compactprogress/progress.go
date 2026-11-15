// SPDX-License-Identifier: MIT

package compactprogress

import (
	"context"
	"sync/atomic"
)

// Progress counts the characters of text and thinking one compaction's summary request has streamed so far. A Session creates one per compaction and carries it on the compaction's context; the interactive status line reads it. It is in-process state: nothing is sent on the wire.
//
// pig divergence (D652): Pi shows no progress while a summary streams; PiG shows the elapsed time and the streamed size on the compaction line.
type Progress struct {
	chars atomic.Int64
}

// charsPerToken is estimateTokens' ratio for text without usage data.
const charsPerToken = 4

// Reset starts the count of the next summary request: the line shows the request in flight, not the sum of the serial requests (chunks, turn prefix, retries) of one compaction.
func (p *Progress) Reset() { p.chars.Store(0) }

// Add counts n streamed characters. It allocates nothing.
func (p *Progress) Add(n int) { p.chars.Add(int64(n)) }

// Tokens is the streamed size in tokens, chars/4 rounded up, as the estimator counts text without usage data.
func (p *Progress) Tokens() int {
	return int((p.chars.Load() + charsPerToken - 1) / charsPerToken)
}

type progressKey struct{}

// With returns ctx carrying p for ProviderCompleter to count into.
func With(ctx context.Context, p *Progress) context.Context {
	return context.WithValue(ctx, progressKey{}, p)
}

// From is the Progress ctx carries, nil when it carries none.
func From(ctx context.Context) *Progress {
	p, _ := ctx.Value(progressKey{}).(*Progress)
	return p
}
