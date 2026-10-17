// SPDX-License-Identifier: MIT

//go:build linux || darwin

// Command pigprobe is cmd/pig with two additions for measurement: SIGUSR1 writes a heap profile (after a collection) to $SESSIONSCALE_HEAP_DIR/heap-<n>.pprof, then an empty heap-<n>.pprof.done marker; SIGUSR2 starts a CPU profile to $SESSIONSCALE_HEAP_DIR/cpu-<n>.pprof and the next SIGUSR2 stops it (then cpu-<n>.pprof.done). Everything else is cli.Main, the code path of the release binary.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"

	"github.com/MichaelKinsy/PiG/coding/cli"
)

func main() {
	dir := os.Getenv("SESSIONSCALE_HEAP_DIR")
	if dir != "" {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGUSR1)
		go func() {
			for n := 1; ; n++ {
				<-sig
				runtime.GC()
				name := filepath.Join(dir, fmt.Sprintf("heap-%d.pprof", n))
				if f, err := os.Create(name); err == nil {
					_ = pprof.Lookup("heap").WriteTo(f, 0)
					_ = f.Close()
				}
				_ = os.WriteFile(name+".done", nil, 0o600)
			}
		}()
		cpu := make(chan os.Signal, 1)
		signal.Notify(cpu, syscall.SIGUSR2)
		go func() {
			for n := 1; ; n++ {
				<-cpu
				name := filepath.Join(dir, fmt.Sprintf("cpu-%d.pprof", n))
				f, err := os.Create(name)
				if err != nil {
					continue
				}
				_ = pprof.StartCPUProfile(f)
				<-cpu
				pprof.StopCPUProfile()
				_ = f.Close()
				_ = os.WriteFile(name+".done", nil, 0o600)
			}
		}()
	}
	cli.Main(os.Args[1:], nil)
}
