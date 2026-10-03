package main

import (
	"fmt"
	"time"

	"github.com/fastschema/qjs"
)

func try(name, code string, opt qjs.Option) {
	t := time.Now()
	res := func() (out string) {
		defer func() {
			if r := recover(); r != nil {
				out = fmt.Sprintf("PANIC %v", r)
			}
		}()
		rt, err := qjs.New(opt)
		if err != nil {
			return "new: " + err.Error()
		}
		defer rt.Close()
		v, err := rt.Eval(name+".js", qjs.Code(code))
		if err != nil {
			return "error: " + err.Error()
		}
		return "ok " + v.String()
	}()
	fmt.Printf("B %-22s %10v  %.150s\n", name, time.Since(t).Round(time.Microsecond), res)
}

func main() {
	t := time.Now()
	rt, err := qjs.New()
	fmt.Printf("B first New (compile, default cache): %v err=%v\n", time.Since(t), err)
	rt.Close()
	t = time.Now()
	const n = 50
	for i := 0; i < n; i++ {
		rt, _ := qjs.New()
		rt.Eval("a.js", qjs.Code("1+1"))
		rt.Close()
	}
	fmt.Printf("B per-execution (New+eval+Close): %v\n", time.Since(t)/n)
	base := qjs.Option{MemoryLimit: 16 << 20, MaxStackSize: 512 * 1024, MaxExecutionTime: 300}
	try("cpu-heavy", "let s=0; for(let i=0;i<50000000;i++) s+=i; s", qjs.Option{MemoryLimit: 16 << 20, MaxStackSize: 512 * 1024})
	try("stack-overflow", "function f(){return f()+1}; try{f()}catch(e){e.name}", base)
	try("memory-limit", "const a=[];for(;;)a.push('x'.repeat(1e6)+a.length)", base)
	try("timeout(300ms)", "for(;;){}", base)
}
