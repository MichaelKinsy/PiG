package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

//go:embed prelude.js
var preludeSource string

type toolFn func(args string) (string, error)

type outcome struct {
	Done    string   // the "done" bridge message, rendered
	Output  []string // text/image items
	Timeout bool
	Err     string
}

func (e *engineA) run(ctx context.Context, code string, tools map[string]toolFn, memoryLimit uint32, timeout time.Duration) (out outcome) {
	v, err := e.newVM(ctx, memoryLimit)
	if err != nil {
		return outcome{Err: err.Error()}
	}
	defer e.closeVM(ctx, v)
	timer := time.AfterFunc(timeout, func() { v.interrupt.Store(true) })
	defer timer.Stop()

	type pend struct {
		id   int
		name string
		args string
	}
	var pending []pend
	var doneSeen bool
	bridge := v.newFunction("bridge", func(this uint32, a []uint32) uint32 {
		kind := v.toString(a[0])
		arg := func(i int) string {
			if i >= len(a) || v.call("qjs_is_undefined", uint64(a[i])) != 0 {
				return ""
			}
			return v.toString(a[i])
		}
		switch kind {
		case "call", "global":
			pending = append(pending, pend{int(math.Float64frombits(v.call("qjs_get_float64", uint64(a[1])))), arg(2), arg(3)})
		case "output":
			out.Output = append(out.Output, arg(1)+":"+arg(2))
		case "done":
			doneSeen = true
			if v.call("qjs_get_bool", uint64(a[1])) != 0 {
				out.Done = "ok value=" + arg(2) + " writes=" + arg(3)
			} else {
				out.Done = "error " + arg(2)
			}
		}
		return v.undefined
	})
	fail := func(err error) outcome {
		if v.interrupt.Load() {
			return outcome{Timeout: true, Output: out.Output}
		}
		return outcome{Err: err.Error(), Output: out.Output}
	}
	toolList := make([]map[string]string, 0, len(tools))
	for name := range tools {
		toolList = append(toolList, map[string]string{"name": name, "jsName": name, "description": name})
	}
	tj, _ := json.Marshal(toolList)
	fnPrelude, err := v.eval(preludeSource, "codemode-prelude.js")
	if err != nil {
		return fail(err)
	}
	api, err := v.callFn(fnPrelude, v.undefined, bridge, v.newString(string(tj)), v.newString("[]"), v.newString("{}"))
	if err != nil {
		return fail(err)
	}
	settle, run, stalled := v.getProp(api, "settle"), v.getProp(api, "run"), v.getProp(api, "stalled")
	drain := func() error {
		if err := v.drain(); err != nil {
			return err
		}
		_, err := v.callFn(stalled, api)
		return err
	}
	fn, err := v.eval("(async (tools, console) => {"+code+"\n})", "codemode.js")
	if err != nil {
		name, rest, _ := strings.Cut(err.Error(), ": ")
		return outcome{Done: "error {\"name\":\"" + name + "\",...}", Output: []string{rest[:min(len(rest), 60)]}}
	}
	if _, err = v.callFn(run, api, fn); err != nil {
		return fail(err)
	}
	if err := drain(); err != nil {
		return fail(err)
	}
	for !doneSeen && len(pending) > 0 {
		p := pending[0]
		pending = pending[1:]
		res, terr := tools[p.name](p.args)
		ok, payload := uint32(1), res
		var okH uint32 = uint32(v.call("qjs_get_true"))
		if terr != nil {
			okH, payload = uint32(v.call("qjs_get_false")), terr.Error()
		}
		_ = ok
		id := uint32(v.call("qjs_new_number", math.Float64bits(float64(p.id))))
		if _, err := v.callFn(settle, api, id, okH, v.newString(payload)); err != nil {
			return fail(err)
		}
		if err := drain(); err != nil {
			return fail(err)
		}
	}
	if !doneSeen {
		return outcome{Err: "no done message", Output: out.Output}
	}
	return outcome{Done: out.Done, Output: out.Output}
}

func (o outcome) String() string {
	return fmt.Sprintf("done=%q output=%q timeout=%v err=%q", trunc(o.Done, 110), o.Output, o.Timeout, trunc(o.Err, 90))
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
