package ai

import (
	"context"
	"sync"
)

// runJoined runs the tasks concurrently, all at once like Promise.all, and waits for all of them. The first failure in time is the error returned. Like
// Promise.all, a failure does not stop the other tasks: they are already issued and run to completion under the caller's context (pi: llama-cpp-classify.ts:307,
// 316-327, 401-404 await Promise.all and pass no AbortController of their own), so the server sees the same requests whichever task fails; only the caller's context cancels them.
func runJoined(ctx context.Context, tasks ...func(context.Context) error) error {
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for _, task := range tasks {
		wg.Go(func() {
			if err := task(ctx); err != nil {
				once.Do(func() { first = err })
			}
		})
	}
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
