package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// listsStep is chapter II: a list on its own worker. Code acting as the owner gets the groceries list's `Todos`: the request goes
// to the list's address, and the first one starts its worker. The same call in a root of the server acts as the server.
func (s *session) listsStep() error {
	groceries := delta.JsonObjectOf("list", "groceries")
	err := s.asRoot(ownerPrincipal, func(ctx host.Ctx) error {
		todos, err := ctx.Get(todos, groceries, host.GetOptions{})
		if err != nil {
			return err
		}
		for _, text := range []string{"milk", "eggs"} {
			if _, err := todos.Call("add", text, ctx); err != nil {
				return err
			}
		}
		if _, err := todos.Call("tick", delta.JsonObjectOf("id", 1, "done", true), ctx); err != nil {
			return err
		}
		items, err := itemsUntil(todos, func(items []todo) bool { return len(items) == 2 && items[0].Done })
		if err != nil {
			return err
		}
		variant, _, err := todos.Variant()
		_, _ = fmt.Fprintln(s.out, "2. as the owner:", variant, "|", show(items))
		return err
	})
	if err != nil {
		return err
	}
	err = s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		todos, err := ctx.Get(todos, groceries, host.GetOptions{})
		if err != nil {
			return err
		}
		if _, err := todos.Call("add", "bread", ctx); err != nil {
			return err
		}
		items, err := itemsUntil(todos, func(items []todo) bool { return len(items) == 3 })
		if err != nil {
			return err
		}
		variant, _, err := todos.Variant()
		_, _ = fmt.Fprintln(s.out, "   as the server:", variant, "|", show(items[2:]))
		return err
	})
	if err != nil {
		return err
	}
	state := s.app.Workers["list"].State().Value()
	addresses := state.(*delta.JsonObject).Keys()
	_, _ = fmt.Fprintln(s.out, "   workers:", strings.Join(addresses, ", "))
	workers := filepath.Join(s.dataDir, "workers", "list")
	directories, err := os.ReadDir(workers)
	if err != nil {
		return err
	}
	for _, directory := range directories {
		files, err := os.ReadDir(filepath.Join(workers, directory.Name()))
		if err != nil {
			return err
		}
		var names []string
		for _, file := range files {
			names = append(names, file.Name())
		}
		slices.Sort(names)
		_, _ = fmt.Fprintf(s.out, "   workers/list/%s: %s\n", directory.Name(), strings.Join(names, ", "))
	}
	return nil
}
