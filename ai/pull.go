//go:build !race

package ai

import "iter"

// pull is iter.Pull. The provider streams run their JavaScript-order turns, async generators and body reads as coroutines on it.
func pull[V any](seq iter.Seq[V]) (next func() (V, bool), stop func()) {
	return iter.Pull(seq)
}
