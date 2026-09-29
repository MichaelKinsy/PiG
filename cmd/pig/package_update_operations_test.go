package main

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
)

// Pi package-manager.ts:50-51,1660-1677 admits four workers and takes the next task in source order. Start events occur before the task's first await.
func TestPackageTasksStartOrderAndJoin(t *testing.T) {
	const concurrency = 4
	tasks := make([]func() error, 2*concurrency)
	entered := make(chan struct{}, len(tasks))
	release := make(chan struct{})
	var finished atomic.Int32
	failure := errors.New("child failed")
	for i := range tasks {
		tasks[i] = func() error {
			entered <- struct{}{}
			<-release
			finished.Add(1)
			if i == len(tasks)-1 {
				return failure
			}
			return nil
		}
	}
	var started []int
	done := make(chan error, 1)
	go func() { done <- runPackageTasks(tasks, func(i int) { started = append(started, i) }) }()
	for range concurrency {
		<-entered
	}
	select {
	case <-entered:
		t.Error("more than four package tasks started before a worker finished")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if int(finished.Load()) != len(tasks) {
		t.Fatalf("returned before all started tasks finished: %d/%d", finished.Load(), len(tasks))
	}
	want := make([]int, len(tasks))
	for i := range want {
		want[i] = i
	}
	if !slices.Equal(started, want) {
		t.Fatalf("start events=%v want=%v", started, want)
	}
}
