package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
)

// userStep is chapter VII: a second user. Ada picks a name (the kit's ephemeral login) and connects a client host of her own over the
// links endpoint, as a page does. A host serves nothing before `Load`: a client with nothing to load calls `Load(nil)`.
func (s *session) userStep() error {
	request, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.origin+app.LoginPath, strings.NewReader(`{"name":"ada"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	login, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	_ = login.Body.Close()
	if len(login.Cookies()) == 0 {
		return fmt.Errorf("ada's login set no cookie (status %d)", login.StatusCode)
	}
	ada, err := host.CreateHost(host.HostOptions{OnError: func(err host.HostError) { fmt.Fprintln(s.out, "ada:", err.Kind) }})
	if err != nil {
		return err
	}
	s.ada = ada
	groceries := delta.JsonObjectOf("list", "groceries")
	if _, err := ada.Hold(s.ctx, todos, groceries, host.GetOptions{}); err != nil {
		fmt.Fprintln(s.out, "7. ada's hold before load:", err)
	}
	if _, err := ada.Load(nil, s.ctx); err != nil {
		return err
	}
	socket, err := dial(s.ctx, s.origin, login.Cookies()[0])
	if err != nil {
		return err
	}
	if err := ada.Connect(link.WebSocketLink(socket), host.LinkOptions{Principal: serverPrincipal}, s.ctx); err != nil {
		return err
	}
	s.adas, err = ada.Get(s.ctx, todos, groceries, host.GetOptions{})
	if err != nil {
		return err
	}
	if _, err := s.adas.Call("add", "coffee", s.ctx); err != nil {
		return err
	}
	variant, _, err := s.adas.Variant()
	fmt.Fprintln(s.out, "   ada's variant:", variant)
	if err != nil {
		return err
	}
	// `clear` is the owner's: Ada's reference has no such member, and the worker would refuse her too.
	if _, err := s.adas.Call("clear", nil, s.ctx); err != nil {
		fmt.Fprintln(s.out, "   ada's clear:", describe(err))
	}
	if err := s.untilGroceries(func(items []todo) bool { return len(items) > 0 && items[len(items)-1].By == "ada" }); err != nil {
		return err
	}
	return s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		return s.showLast("   the server's reference sees:", ctx)
	})
}

// describe is an error as `code/reason`.
func describe(err error) string {
	if found, ok := data.IsChordError(err); ok {
		return fmt.Sprintf("%s/%s", found.Code, found.Reason)
	}
	return err.Error()
}
