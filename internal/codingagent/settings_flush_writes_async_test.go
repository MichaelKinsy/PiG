package codingagent

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"runtime"
	"testing"
	"time"
)

// FlushSettingsWrites is what a process calls before it exits: it returns only after the write another goroutine's setter is performing, for every manager (settings-manager.ts flush, :670-672). The test runs outside a synctest bubble because the set of writers is the process's.
func TestFlushSettingsWritesWaitsForAWriteInProgress(t *testing.T) {
	entered, gate := make(chan struct{}), make(chan struct{})
	storage := &hookedSettingsStorage{hook: func(_ string, write int) error {
		if write == 1 {
			close(entered)
			<-gate
		}
		return nil
	}}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	set := make(chan error, 1)
	go func() { set <- manager.SetTheme("queued") }()
	<-entered
	// The stored theme when FlushSettingsWrites returned.
	var seen any
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		FlushSettingsWrites()
		seen = storage.stored("global", "theme")
	}()
	select {
	case <-flushed:
		t.Fatalf("FlushSettingsWrites returned while a write was in progress (stored theme %v)", seen)
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	<-flushed
	if err := <-set; err != nil {
		t.Fatal(err)
	}
	if seen != "queued" {
		t.Fatalf("stored theme when FlushSettingsWrites returned = %v, want queued", seen)
	}
}

// A write that ends its goroutine (a storage that calls runtime.Goexit, as a failed test helper does) releases the queue: the setters after it are not stranded behind it.
func TestSettingsWriteThatEndsItsGoroutineDoesNotStrandTheQueue(t *testing.T) {
	storage := &hookedSettingsStorage{hook: func(_ string, write int) error {
		if write == 1 {
			runtime.Goexit()
		}
		return nil
	}}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = manager.SetTheme("lost")
	}()
	<-ended
	done := make(chan error, 1)
	go func() { done <- manager.SetShellPath("/after") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a setter after a write that ended its goroutine never returned")
	}
	if got := storage.stored("global", "shellPath"); got != "/after" {
		t.Fatalf("stored shellPath = %v, want /after", got)
	}
	flushed := make(chan struct{})
	go func() { FlushSettingsWrites(); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("FlushSettingsWrites never returned after a write that ended its goroutine")
	}
}
