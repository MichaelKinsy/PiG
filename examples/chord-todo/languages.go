package main

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// messages are the lists plugin's English messages for the Go server (`plugins/lists/messages.ts` has them for plugins, in English and
// German): a Go program is no plugin, so it renders with a catalog of its own, as a server renders text for a log line.
var messages = func() *surfaces.Messages {
	english := surfaces.LocaleCatalog{Locale: "en", Catalog: surfaces.Catalog{
		"cleared": surfaces.MessageFunc(func(_ *surfaces.Language, args ...any) (string, error) {
			return fmt.Sprintf("%v cleared the done todos", args[0]), nil
		}),
	}}
	defined, err := surfaces.DefineMessages("todo.lists", english)
	if err != nil {
		panic(err)
	}
	return defined
}()

// chooses has principal choose locale: a preference the server keeps per principal, which every client of that principal follows.
func (s *session) chooses(principal data.Principal, locale string) error {
	return s.asRoot(principal, func(ctx host.Ctx) error {
		choice, err := ctx.Get(surfaces.LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return err
		}
		_, err = choice.Call("choose", locale, ctx)
		return err
	})
}

// languagesStep is chapter VIII. The owner chooses German: a preference the server keeps per principal, which every client of the
// owner follows, so the terminal redraws in German without restarting. Ada's pick is her own. Clearing the done todos leaves a notice
// as data, a key and its parameters: the Go server renders it with its English catalog, the terminal renders the same state in German.
func (s *session) languagesStep() error {
	if err := s.chooses(ownerPrincipal, "de"); err != nil {
		return err
	}
	fmt.Fprintln(s.out, "8. the owner chooses German")
	if s.terminal != nil {
		if err := until(func() (bool, error) { return s.terminal.showsScreen("Einkäufe"), nil }); err != nil {
			return err
		}
		fmt.Fprintln(s.out, "   the terminal shows:")
		for _, line := range s.terminal.screen() {
			fmt.Fprintln(s.out, "  ", line)
		}
	}
	choice, err := s.ada.Get(s.ctx, surfaces.LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer choice.Release()
	if _, err := choice.Call("choose", "fr", s.ctx); err != nil {
		return err
	}
	pick, err := choice.Member("pick").Value()
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, "   ada's pick:", pick)
	err = s.asRoot(ownerPrincipal, func(ctx host.Ctx) error {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err != nil {
			return err
		}
		_, err = list.Call("clear", nil, ctx)
		return err
	})
	if err != nil {
		return err
	}
	var notice surfaces.MessageData
	err = s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err != nil {
			return err
		}
		return until(func() (bool, error) {
			value, err := list.Member("notice").Value()
			if err != nil || value == nil {
				return false, nil
			}
			text, err := json.Marshal(value)
			if err != nil {
				return false, err
			}
			return true, json.Unmarshal(text, &notice)
		})
	})
	if err != nil {
		return err
	}
	text, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, "   the notice:", string(text))
	fmt.Fprintln(s.out, "   the server renders it:", messages.Render(notice))
	if s.terminal != nil {
		if err := until(func() (bool, error) { return s.terminal.showsScreen("entfernt"), nil }); err != nil {
			return err
		}
		lines := s.terminal.screen()
		fmt.Fprintln(s.out, "   the terminal renders it:", lines[len(lines)-1])
	}
	return nil
}
