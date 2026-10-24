//go:build windows

package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Throwaway measurements, identical for the old and the new waiter: they use only ReadInputStream, newTerminalReader and read.

func BenchmarkDiagPipeQueued(b *testing.B) {
	r, w, err := os.Pipe()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, err := newTerminalReader(ctx, r)
	if err != nil {
		b.Fatal(err)
	}
	defer reader.close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := w.Write([]byte{'k'}); err != nil {
			b.Fatal(err)
		}
		if data, err := reader.read(-1); err != nil || len(data) != 1 {
			b.Fatalf("read = %q, %v", data, err)
		}
	}
}

func BenchmarkDiagPipeKeystroke(b *testing.B) {
	r, w, err := os.Pipe()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ReadInputStream(ctx, r, func([]byte) { got <- struct{}{} }) }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := w.Write([]byte{'k'}); err != nil {
			b.Fatal(err)
		}
		<-got
	}
	b.StopTimer()
	_ = w.Close() // frees the old reader, which cannot be cancelled
	cancel()
	<-done
}

var procGetProcessHandleCount = kernel32.NewProc("GetProcessHandleCount")

func handleCount() uint32 {
	var n uint32
	_, _, _ = procGetProcessHandleCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n))) //nolint:gosec
	return n
}

func settledGoroutines() int {
	n := runtime.NumGoroutine()
	for range 100 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		m := runtime.NumGoroutine()
		if m == n {
			break
		}
		n = m
	}
	return n
}

// cycle starts a reader on a fresh pipe, delivers one key, cancels it, and reports whether the reader ended within limit with the writer still open.
func cycle(closeWriterFirst bool, limit time.Duration) (endedWithWriterOpen bool) {
	r, w, _ := os.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ReadInputStream(ctx, r, func([]byte) { got <- struct{}{} }) }()
	_, _ = w.Write([]byte{'k'})
	<-got
	cancel()
	if closeWriterFirst {
		_ = w.Close()
		<-done
		endedWithWriterOpen = true
	} else {
		select {
		case <-done:
			endedWithWriterOpen = true
		case <-time.After(limit):
			_ = w.Close() // free the parked reader so the cycle can finish
			<-done
		}
	}
	_ = w.Close()
	_ = r.Close()
	return endedWithWriterOpen
}

func TestDiagLifecycle(t *testing.T) {
	cycle(true, 0) // warm up lazily created state
	g0, h0 := settledGoroutines(), handleCount()
	const n = 200
	for range n {
		cycle(true, 0)
	}
	g1, h1 := settledGoroutines(), handleCount()
	fmt.Printf("LIFECYCLE writer closed before join, %d cycles: goroutines %d -> %d, handles %d -> %d\n", n, g0, g1, h0, h1)
	const m = 20
	stopped := 0
	for range m {
		if cycle(false, time.Second) {
			stopped++
		}
	}
	g2, h2 := settledGoroutines(), handleCount()
	fmt.Printf("LIFECYCLE cancel with the writer open, %d cycles: reader ended on cancel %d of %d; goroutines %d -> %d, handles %d -> %d\n", m, stopped, m, g1, g2, h1, h2)
}

var procGetProcessTimes = kernel32.NewProc("GetProcessTimes")

func processCPU() time.Duration {
	var c, e, k, u windows.Filetime
	_, _, _ = procGetProcessTimes.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&c)), uintptr(unsafe.Pointer(&e)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))) //nolint:gosec
	d := func(f windows.Filetime) time.Duration { return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100 }
	return d(k) + d(u)
}

func TestDiagIdleCPU(t *testing.T) {
	for _, idle := range []time.Duration{time.Second, time.Second, time.Second} {
		r, w, _ := os.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- ReadInputStream(ctx, r, func([]byte) {}) }()
		time.Sleep(100 * time.Millisecond) // the reader is parked in its wait
		before := processCPU()
		time.Sleep(idle)
		used := processCPU() - before
		_ = w.Close()
		cancel()
		<-done
		_ = r.Close()
		fmt.Printf("IDLECPU reader on an empty pipe for %v: process CPU %v\n", idle, used)
	}
	// A reader with a framing deadline pending wakes on it; only the new waiter can time a pipe wait out.
	r, w, _ := os.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	expiries := 0
	done := make(chan error, 1)
	go func() {
		done <- ReadInputStreamUntil(ctx, r, func() time.Time { return time.Now().Add(50 * time.Millisecond) }, func([]byte) {}, func() { mu.Lock(); expiries++; mu.Unlock() })
	}()
	time.Sleep(100 * time.Millisecond)
	before := processCPU()
	time.Sleep(time.Second)
	used := processCPU() - before
	mu.Lock()
	n := expiries
	mu.Unlock()
	_ = w.Close()
	cancel()
	<-done
	_ = r.Close()
	fmt.Printf("IDLECPU reader with a 50 ms framing deadline for 1s: %d expiries, process CPU %v\n", n, used)
}

const diagConsoleChildEnv = "PIG_DIAG_CONSOLE_CHILD"

func TestDiagConsoleKeystroke(t *testing.T) {
	const keys = 300
	if dir := os.Getenv(diagConsoleChildEnv); dir != "" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		restore, err := NewProcessTerminal(os.Stdin, os.Stdout).EnterRawMode()
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
		count := 0
		done := make(chan error, 1)
		go func() {
			done <- ReadInputStream(ctx, os.Stdin, func(data []byte) {
				for range data {
					_, _ = os.Stdout.WriteString("\u00ab")
					count++
				}
				if count >= keys+1 {
					cancel()
				}
			})
		}()
		_ = os.WriteFile(dir+".ready", nil, 0o600)
		<-done
		return
	}
	dir := t.TempDir() + `\diag`
	t.Setenv(diagConsoleChildEnv, dir)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestDiagConsoleKeystroke$", "-test.count=1")
	console.waitForFile(t, dir+".ready")
	ack := func(want int) {
		deadline := time.Now().Add(20 * time.Second)
		for strings.Count(console.output(), "\u00ab") < want {
			if time.Now().After(deadline) {
				t.Fatalf("no acknowledgement of key %d", want)
			}
			runtime.Gosched()
		}
	}
	console.write(t, "k")
	ack(1)
	start := time.Now()
	for i := 2; i <= keys+1; i++ {
		console.write(t, "k")
		ack(i)
	}
	per := time.Since(start) / keys
	fmt.Printf("BenchmarkDiagConsoleKeystroke\t%d\t%d ns/op\n", keys, per.Nanoseconds())
	console.wait(t)
	_ = io.Discard
}
