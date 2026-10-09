// Ports packages/agent/src/agent-loop.ts
package asyncorder

import "sync"

func bad(f func()) {
	go f() // want `goroutine hand-off in code ported from packages/agent/src/agent-loop.ts`
}

func joined(f func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); f() }()
	wg.Wait()
}
