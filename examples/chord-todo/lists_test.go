package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// The lists plugin's own test (chapter XII): the plugin built as production builds it, evaluated with the app's real API module and
// loaded alone on a host of its own at a list's address, so it may serve `Todos`. Each case runs code as a principal of its choice.
// A Go test drives it from outside, through a server host that routes the list kind to the plugin's host, as the app's server does.

// testedList is the lists plugin tested on the worker of the list "groceries".
type testedList struct {
	tested *plugins.TestedPlugin
	server host.Host
}

func testList(t *testing.T) *testedList {
	t.Helper()
	address := listKind.Address(delta.JsonObjectOf("id", "groceries"))
	tested, err := plugins.TestPlugin(filepath.Join(here, "plugins", "lists"), plugins.TestPluginOptions{
		Build:     build.BuildFacet,
		Kind:      "worker",
		Kinds:     []string{"server", "worker", "web", "tui"},
		Api:       &model.AppApi{Name: "@todo/app", Versions: []string{"v1"}},
		Modules:   plugins.HostPackages{"@todo/app/v1": filepath.Join(here, "api", "v1.ts")},
		Address:   &address,
		Principal: workerPrincipal(address),
		Node:      plugins.NodeOptions{CacheDir: t.TempDir()},
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tested.Close(context.Background()) })
	server, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	var resolved atomic.Bool
	if _, err := server.Route(listKind, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			if !resolved.CompareAndSwap(false, true) {
				return host.ResolvedLink{}, errors.New("the worker's link was attached already")
			}
			return host.ResolvedLink{Link: tested.Host.Link(), Principal: workerPrincipal(address), Trusted: true}, nil
		},
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Load(nil, context.Background()); err != nil {
		t.Fatal(err)
	}
	return &testedList{tested: tested, server: server}
}

// root runs fn as principal, with the list's `Todos` of "groceries" taken as that principal sees it.
func (l *testedList) root(t *testing.T, principal data.Principal, fn func(todos *host.Reference, ctx host.Ctx) error) {
	t.Helper()
	_, err := l.server.Root(func(ctx host.Ctx) (any, error) {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return nil, fn(list, ctx)
	}, context.Background(), host.RootOptions{Principal: principal})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheListsPluginAddsATodoAsThePrincipalThatCalled(t *testing.T) {
	ada := delta.JsonObjectOf("kind", "user", "id", "u1", "name", "ada")
	testList(t).root(t, ada, func(list *host.Reference, ctx host.Ctx) error {
		if variant, _, err := list.Variant(); err != nil || variant != "member" {
			t.Errorf("variant = %q, %v; want member", variant, err)
		}
		if _, err := list.Call("add", "coffee", ctx); err != nil {
			return err
		}
		items, err := itemsUntil(list, func(items []todo) bool { return len(items) == 1 })
		if got := show(items); got != "[ ] coffee (ada)" {
			t.Errorf("items = %q", got)
		}
		return err
	})
}

func TestTheListsPluginRefusesATodoWithoutTextWithInvalidArgs(t *testing.T) {
	ada := delta.JsonObjectOf("kind", "user", "id", "u1", "name", "ada")
	testList(t).root(t, ada, func(list *host.Reference, ctx host.Ctx) error {
		_, err := list.Call("add", "", ctx)
		if got := describe(err); got != "invalid/args" {
			t.Errorf("refused = %q (%v), want invalid/args", got, err)
		}
		return nil
	})
}

func TestTheListsPluginLetsTheOwnerClearTheDoneTodos(t *testing.T) {
	l := testList(t)
	l.root(t, ownerPrincipal, func(list *host.Reference, ctx host.Ctx) error {
		if variant, _, err := list.Variant(); err != nil || variant != "owner" {
			t.Errorf("the owner got %q, %v", variant, err)
		}
		for _, text := range []string{"milk", "eggs"} {
			if _, err := list.Call("add", text, ctx); err != nil {
				return err
			}
		}
		if _, err := list.Call("tick", delta.JsonObjectOf("id", 1, "done", true), ctx); err != nil {
			return err
		}
		if _, err := list.Call("clear", nil, ctx); err != nil {
			return err
		}
		items, err := itemsUntil(list, func(items []todo) bool { return len(items) == 1 })
		if got := show(items); got != "[ ] eggs (owner)" {
			t.Errorf("items = %q", got)
		}
		return err
	})
	unserved, err := l.tested.Unserved(context.Background())
	if err != nil || len(unserved) != 0 {
		t.Fatalf("unserved = %v, %v; want none", unserved, err)
	}
}
