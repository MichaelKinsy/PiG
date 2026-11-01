package plugins

import (
	"context"
	"testing"
	"time"
)

// Not upstream's: what it costs to start a Node subprocess for a plugin host (docs/plan/chord-plugins-node-evaluator.md, 10.8).
// Upstream's plugins run in the app's process, so it has no start to measure. Run by hand (not in CI):
//
//	go test -run '^$' -bench BenchmarkNodeStart -benchtime 10x ./chord/nextnode/plugins/
//
// Each case starts a process with no plugins and reports, per start, the milliseconds from the call to `started` (the process
// connected and its Pi modules imported: startNode's return, which includes finding Node with `node --version`) and to the
// `load` report (what PluginRuntime.Load returns). `cold` extracts the embedded tree into a new cache directory for each
// start (and so starts with an empty V8 compile cache); `warm` starts from a tree one earlier start populated. `no-v8-cache`
// sets NODE_DISABLE_COMPILE_CACHE for the subprocess, which makes `module.enableCompileCache()` of the driver a no-op: the
// difference to `v8-cache` is the compile cache's. `warm/spare` takes the process PluginRuntime.Warm started before the timer ran:
// the start's cost that is left on the critical path.
func BenchmarkNodeStart(b *testing.B) {
	for _, tree := range []string{"cold", "warm"} {
		for _, cache := range []string{"v8-cache", "no-v8-cache"} {
			b.Run(tree+"/"+cache, func(b *testing.B) {
				if cache == "no-v8-cache" {
					b.Setenv("NODE_DISABLE_COMPILE_CACHE", "1")
				}
				shared := b.TempDir()
				options := func() NodeOptions {
					if tree == "cold" {
						return NodeOptions{CacheDir: b.TempDir()}
					}
					return NodeOptions{CacheDir: shared}
				}
				if tree == "warm" {
					// One start fills the tree and the compile cache.
					node, _, err := NewPluginRuntime(options()).Load(benchLoad(), context.Background())
					if err != nil {
						b.Fatal(err)
					}
					_ = node.Close(context.Background())
				}
				var started, loaded time.Duration
				for b.Loop() {
					runtime := NewPluginRuntime(options())
					begin := time.Now()
					node, err := startNode(runtime.node, context.Background())
					if err != nil {
						b.Fatal(err)
					}
					started += time.Since(begin)
					if err := loadBenchNode(node); err != nil {
						b.Fatal(err)
					}
					loaded += time.Since(begin)
					b.StopTimer()
					_ = node.Close(context.Background())
					b.StartTimer()
				}
				b.ReportMetric(float64(started)/float64(time.Millisecond)/float64(b.N), "ms/started")
				b.ReportMetric(float64(loaded)/float64(time.Millisecond)/float64(b.N), "ms/loaded")
			})
		}
	}
	b.Run("warm/spare", func(b *testing.B) {
		runtime := NewPluginRuntime(NodeOptions{CacheDir: b.TempDir()})
		defer func() { _ = runtime.Close(context.Background()) }()
		// One start fills the tree and the compile cache.
		node, _, err := runtime.Load(benchLoad(), context.Background())
		if err != nil {
			b.Fatal(err)
		}
		_ = node.Close(context.Background())
		var loaded time.Duration
		for b.Loop() {
			b.StopTimer()
			if err := runtime.Warm(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			begin := time.Now()
			node, _, err := runtime.Load(benchLoad(), context.Background())
			if err != nil {
				b.Fatal(err)
			}
			loaded += time.Since(begin)
			b.StopTimer()
			_ = node.Close(context.Background())
			b.StartTimer()
		}
		b.ReportMetric(float64(loaded)/float64(time.Millisecond)/float64(b.N), "ms/loaded")
	})
	b.Run("discover", func(b *testing.B) {
		for b.Loop() {
			if _, err := discoverNode(context.Background(), nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// benchLoad is a load of a host with no plugins.
func benchLoad() RuntimeLoad {
	return RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}
}

// loadBenchNode sends the load of benchLoad to a started node and waits for its report.
func loadBenchNode(node *NodeHost) error {
	request := &loadRequest{Kind: "worker", Core: []coreWire{}, Scripts: scriptsWire(nil), Modules: map[string]string{}, Principal: workerPrincipal, Peer: serverPrincipal}
	call, err := node.send("load", request)
	if err != nil {
		return err
	}
	_, err = node.await(call, context.Background())
	return err
}
