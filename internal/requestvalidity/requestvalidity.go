// Package requestvalidity checks the requests PiG sends to model providers against each provider's documented message contract.
//
// The checker is inert in a production binary. It runs in every Go test binary and in a binary built with the requestvalidity tag. A violation is a bug in the code that built the request, whatever the provider would answer: it ends the process with a stack trace (a panic on a goroutine nothing can recover), so the test or scenario that sent the request fails even when an agent loop wraps the provider call. PIG_REQUEST_VALIDITY_REPORT=<file> appends one line per violation to the file and continues, to survey a whole suite.
package requestvalidity

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Violation is one broken rule of a provider's message contract.
type Violation struct {
	// Rule names the broken rule.
	Rule string
	// Detail states which messages break it.
	Detail string
}

func (v Violation) String() string { return v.Rule + ": " + v.Detail }

var (
	enabledOnce sync.Once
	enabled     bool
	reportMu    sync.Mutex
)

// Enabled reports whether provider requests are checked: in a test binary or a requestvalidity-tagged build.
func Enabled() bool {
	enabledOnce.Do(func() { enabled = taggedBuild || testing.Testing() })
	return enabled
}

type allowKey struct{}

// Allow returns a context whose requests are not checked. A test that sends an invalid request on purpose, to observe how a provider answers it, uses it.
func Allow(ctx context.Context) context.Context { return context.WithValue(ctx, allowKey{}, true) }

var (
	globalMu        sync.Mutex
	globalCollector *Collector
)

// CollectAll records the violations of every request the process sends until the test ends, including requests whose context does not carry a collector: a Session's agent loop runs a provider call under its own context. Only one test at a time may collect, and tests that run in parallel with it see their violations recorded too.
func CollectAll(t testing.TB) *Collector {
	t.Helper()
	collector := &Collector{}
	globalMu.Lock()
	if globalCollector != nil {
		globalMu.Unlock()
		t.Fatal("requestvalidity: another test is collecting")
	}
	globalCollector = collector
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		globalCollector = nil
		globalMu.Unlock()
	})
	return collector
}

type collectorKey struct{}

// Collector receives the violations of the requests sent under its context, instead of the panic Enforce raises. A fuzz or exploration harness that needs the verdict of every request uses it.
type Collector struct {
	mu         sync.Mutex
	violations []string
}

// Collect returns a context whose invalid requests are recorded in the collector.
func Collect(ctx context.Context) (context.Context, *Collector) {
	collector := &Collector{}
	return context.WithValue(ctx, collectorKey{}, collector), collector
}

// Violations returns the recorded violations as "api rule: detail" lines.
func (c *Collector) Violations() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.violations)
}

func allowed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ok, _ := ctx.Value(allowKey{}).(bool)
	return ok
}

// Enforce fails the request that built an invalid message list.
func Enforce(ctx context.Context, api string, violations []Violation) {
	if len(violations) == 0 || allowed(ctx) {
		return
	}
	var collector *Collector
	if ctx != nil {
		collector, _ = ctx.Value(collectorKey{}).(*Collector)
	}
	if collector == nil {
		globalMu.Lock()
		collector = globalCollector
		globalMu.Unlock()
	}
	if collector != nil {
		collector.mu.Lock()
		for _, v := range violations {
			collector.violations = append(collector.violations, api+" "+v.String())
		}
		collector.mu.Unlock()
		return
	}
	var text strings.Builder
	for _, v := range violations {
		fmt.Fprintf(&text, "\n  - %s", v)
	}
	if path := os.Getenv("PIG_REQUEST_VALIDITY_REPORT"); path != "" {
		report(path, api, violations)
		return
	}
	fail(fmt.Sprintf("requestvalidity: invalid %s request:%s", api, text.String()))
}

// fail ends the process with the violation and a stack trace. A plain panic would not do: an agent loop recovers a provider's panic into an error turn, and a test that does not read that turn would pass.
func fail(message string) {
	go func() { panic(message) }() //portlint:allow golifetime the goroutine exists only to panic unrecovered and end the process (doc above)
	select {}
}

func report(path, api string, violations []Violation) {
	reportMu.Lock()
	defer reportMu.Unlock()
	var lines strings.Builder
	for _, v := range violations {
		detail := strings.ReplaceAll(v.Detail, "\n", " ")
		fmt.Fprintf(&lines, "%s\t%s\t%s\t%s\n", api, v.Rule, detail, os.Args[0])
		fmt.Fprintf(os.Stderr, "REQUESTVALIDITY %s %s: %s\n", api, v.Rule, detail)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(fmt.Sprintf("requestvalidity: open report %q: %v", path, err))
	}
	if _, err := file.WriteString(lines.String()); err != nil {
		panic(fmt.Sprintf("requestvalidity: write report %q: %v", path, err))
	}
	if err := file.Close(); err != nil {
		panic(fmt.Sprintf("requestvalidity: close report %q: %v", path, err))
	}
}
