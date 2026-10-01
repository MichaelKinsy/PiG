package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

var scenarios = []struct{ name, code string }{
	{"return", "return 1 + 1"},
	{"tool", "return await tools.add({ a: 1, b: 2 })"},
	{"output", "text('hi'); console.log('log', 3); return 'x'"},
	{"syntax-error", "return ("},
	{"throw", "throw new TypeError('boom')"},
	{"stalled", "await new Promise(() => {})"},
	{"stack-overflow-caught", "function f() { return f() + 1 } try { f() } catch (e) { return e.name }"},
	{"memory-limit", "const a = []; for (;;) a.push('x'.repeat(1e6) + a.length)"},
	{"timeout", "for (;;) {}"},
}

func main() {
	ctx := context.Background()
	tools := map[string]toolFn{"add": func(args string) (string, error) { return "3", nil }}
	cacheDir := os.Args[1]
	t0 := time.Now()
	e, err := newEngineA(ctx, cacheDir)
	if err != nil {
		panic(err)
	}
	fmt.Printf("A engine init+compile (cache dir %q): %v\n", cacheDir, time.Since(t0))
	for _, s := range scenarios {
		t := time.Now()
		o := e.run(ctx, s.code, tools, 16<<20, 300*time.Millisecond)
		fmt.Printf("A %-22s %8v  %s\n", s.name, time.Since(t).Round(time.Microsecond), o)
	}
	// per-execution overhead
	const n = 50
	t := time.Now()
	for i := 0; i < n; i++ {
		e.run(ctx, "return 1", tools, 16<<20, 5*time.Second)
	}
	fmt.Printf("A per-execution (return 1): %v\n", time.Since(t)/n)
	t = time.Now()
	o := e.run(ctx, "let s=0; for(let i=0;i<50000000;i++) s+=i; return s", tools, 16<<20, 60*time.Second)
	fmt.Printf("A cpu-heavy 5e7 loop: %v  %s\n", time.Since(t), o)
}
