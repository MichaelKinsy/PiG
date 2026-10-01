package ai

import (
	"context"
	"sync"
)

// runJoined runs the tasks concurrently, all at once like Promise.all, and waits for all of them. The first failure cancels
// the context the other tasks see and is the error returned. It is Promise.all with owned, joined concurrency.
func runJoined(ctx context.Context, tasks ...func(context.Context) error) error {
	joined, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for _, task := range tasks {
		wg.Go(func() {
			if err := task(joined); err != nil {
				once.Do(func() {
					first = err
					cancel(err)
				})
			}
		})
	}
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
