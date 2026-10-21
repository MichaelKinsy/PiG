package ai

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// ai is outside every ordering scope: none of these shapes is flagged by any ordering check.

type provider struct {
	onEvent func(name string)
	handler extension.Handler
}

func newProvider() *provider {
	p := &provider{}
	go func() { p.onEvent("x") }()
	go func() { p.handler.Handle("x") }()
	return p
}

func (p *provider) fanIn(m map[string]func(string)) {
	ch := make(chan string, 4)
	for k, cb := range m {
		cb(k)
		ch <- k
		emit(k)
	}
}

func emit(string) {}

func out() { fmt.Fprintln(os.Stdout, "x") }
