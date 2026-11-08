package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/chord/nextnode/tui"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
)

// session is what the steps after the first share: the running app, its address and the owner's cookie.
type session struct {
	ctx     context.Context
	out     io.Writer
	app     *app.App
	dataDir string
	store   *telemetrysqlite.SqliteTelemetry
	// root is the plugins root, base the directory holding it and the data directory.
	root, base string
	origin     string
	// token is the owner token file's content, for the command-line client's requests.
	token string
	// terminal is the scripted terminal of chapter V, and running its client; nil without a Node for the sandbox.
	terminal *scripted
	running  *tui.RunningTerminal
	// ada is Ada's client host (chapter VII) and adas her reference to the groceries list.
	ada  host.Host
	adas *host.Reference
}

// stop ends the terminal client.
func (s *session) stop() {
	if s.adas != nil {
		s.adas.Release()
	}
	if s.ada != nil {
		_ = s.ada.Close(s.ctx)
	}
	if s.running != nil {
		s.running.Stop()
	}
}

// asRoot runs fn in a root of the server's host acting as principal: a call scope for code outside objects, whose `ctx`
// carries the principal. The references `ctx.Get` takes end when fn returns.
func (s *session) asRoot(principal data.Principal, fn func(ctx host.Ctx) error) error {
	_, err := s.app.Host.Root(func(ctx host.Ctx) (any, error) { return nil, fn(ctx) }, s.ctx, host.RootOptions{Principal: principal})
	return err
}

// until waits until check holds.
func until(check func() (bool, error)) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ok, err := check()
		if err != nil || ok {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// itemsUntil reads the `items` of a Todos reference until ok holds of them.
func itemsUntil(ref *host.Reference, ok func([]todo) bool) ([]todo, error) {
	var items []todo
	err := until(func() (bool, error) {
		value, err := ref.Member("items").Value()
		if err != nil {
			return false, nil // not live yet
		}
		if items, err = itemsOf(value); err != nil {
			return false, err
		}
		return ok(items), nil
	})
	return items, err
}

// cli is the headers of a command-line client on this machine: the owner token file sent as a bearer token.
func (s *session) cli() map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.token}
}

// request sends an HTTP request to the app with headers and returns the status and the body.
func (s *session) request(method, path string, body []byte, headers map[string]string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(s.ctx, method, s.origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	return response.StatusCode, answer, err
}

// untilGroceries reads the groceries list as the server's own reference sees it until ok holds of its todos.
func (s *session) untilGroceries(ok func([]todo) bool) error {
	return s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err != nil {
			return err
		}
		_, err = itemsUntil(list, ok)
		return err
	})
}

// showLast prints the label and the groceries' last todo, as the server's root reads it.
func (s *session) showLast(label string, ctx host.Ctx) error {
	list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
	if err != nil {
		return err
	}
	items, err := itemsUntil(list, func(items []todo) bool { return len(items) > 0 })
	_, _ = fmt.Fprintln(s.out, label, show(items[len(items)-1:]))
	return err
}
