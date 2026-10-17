package gorecover

import "sync"

func bad(items []string, wg *sync.WaitGroup) {
	for _, it := range items {
		go func() { // want `fan-out goroutine has no deferred recover`
			defer wg.Done()
			_ = it
		}()
	}
}

func good(items []string, wg *sync.WaitGroup) {
	for _, it := range items {
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }()
			_ = it
		}()
	}
}

func notLoop(wg *sync.WaitGroup) {
	go func() { defer wg.Done() }()
}
