package jsloop

import (
	"fmt"
	"os"
)

// internal/jsloop implements the primitives: none of these shapes is flagged by any ordering check.

type loop struct{ queue []func() }

func (l *loop) Macrotask(fn func()) {
	go func() { fn() }()
}

func newLoop() *loop {
	l := &loop{}
	go l.run()
	return l
}

func (l *loop) run() {}

func (l *loop) fanIn(m map[string]func(string)) {
	ch := make(chan string, 4)
	for k, cb := range m {
		cb(k)
		ch <- k
	}
}

func out() { fmt.Fprintln(os.Stdout, "x") }
