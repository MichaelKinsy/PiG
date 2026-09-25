package ai

import (
	"sync"
	"testing"
)

// TestOAuthRegistry_ConcurrentAccess hammers the custom registry from many
// goroutines. Under `go test -race` it reports a data race on the map unless
// the registry is mutex-guarded. Register/Unregister model the extension-host
// load/shutdown lifecycle; Get models model resolution and the /login selector
// reading concurrently.
func TestOAuthRegistry_ConcurrentAccess(t *testing.T) {
	t.Cleanup(ResetOAuthProviders)
	const n = 50
	var wg sync.WaitGroup
	for range n {
		wg.Add(4)
		go func() { defer wg.Done(); RegisterOAuthProvider("race", fakeOAuthProvider{}) }()
		go func() { defer wg.Done(); _, _ = GetOAuthProvider("race") }()
		go func() { defer wg.Done(); _ = GetOAuthProviders() }()
		go func() { defer wg.Done(); UnregisterOAuthProvider("race") }()
	}
	wg.Wait()
}
