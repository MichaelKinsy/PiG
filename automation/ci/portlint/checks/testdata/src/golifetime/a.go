package golifetime

import (
	"context"
	"sync"
	"time"
)

func badAfter(ch chan int, done chan struct{}) {
	for {
		select {
		case <-ch:
		case <-done:
			return
		case <-time.After(time.Second): // want `time.After in a loop`
		}
	}
}

func badSelect(a, b chan int) {
	for {
		select { // want `select in a loop with no ctx.Done`
		case <-a:
		case <-b:
		}
	}
}

func goodSelect(ctx context.Context, a chan int) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a:
		}
	}
}

func badGo() {
	go func() { // want `goroutine has no context, WaitGroup`
		println("x")
	}()
}

func goodGo(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
	}()
}

func goodGoCtx(ctx context.Context) {
	go func() { <-ctx.Done() }()
}
