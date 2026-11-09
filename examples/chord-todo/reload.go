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

// reloadStep is chapter IX: an edit applied in place. The lists plugin's worker facet trims a todo's text, so a blank one is empty and
// the store refuses it with Chord's `invalid/args`, which reaches Ada as it would a refused argument. The watcher marks the plugin
// dirty; applying the changes rolls it out to the server, the list's worker, the page and the terminal. On the worker each object of
// the plugin restarts: running calls finish on the old code, new calls wait for the new code, documents and tables stay (the store's
// live query runs again on its new attempt).
func (s *session) reloadStep() error {
	const before = "add: (text: string, ctx: Ctx) => store.add({ text, by }, ctx),"
	const after = "add: (text: string, ctx: Ctx) => store.add({ text: text.trim(), by }, ctx),"
	workerFile := filepath.Join(s.root, "lists", "worker.ts")
	source, err := os.ReadFile(workerFile)
	if err != nil {
		return err
	}
	if err := os.WriteFile(workerFile, []byte(strings.Replace(string(source), before, after, 1)), 0o644); err != nil {
		return err
	}
	if err := until(func() (bool, error) {
		return slices.Contains(s.app.Plugins.Component.Marks().Dirty, "@todo/lists"), nil
	}); err != nil {
		return err
	}
	applied, err := s.app.ApplyChanges(s.ctx)
	if err != nil {
		return err
	}
	var steps []string
	for _, step := range applied.Steps {
		steps = append(steps, step.Name)
	}
	_, _ = fmt.Fprintln(s.out, "9. lists changed, rolled out to:", strings.Join(steps, ", "))
	for _, step := range applied.Steps {
		if step.Name != "list" {
			continue
		}
		for _, rolled := range step.Hosts {
			var fates []string
			for _, object := range rolled.Report.Objects {
				if fate := object.Object + " " + string(object.Kind); !slices.Contains(fates, fate) {
					fates = append(fates, fate)
				}
			}
			_, _ = fmt.Fprintf(s.out, "   on %s: %s\n", rolled.Host, strings.Join(fates, ", "))
		}
	}
	if _, err := s.adas.Call("add", "  tea  ", s.ctx); err != nil {
		return err
	}
	if err := s.untilGroceries(func(items []todo) bool { return len(items) > 0 && items[len(items)-1].Text == "tea" }); err != nil {
		return err
	}
	err = s.asRoot(serverPrincipal, func(ctx host.Ctx) error { return s.showLast("   ada adds '  tea  ':", ctx) })
	if err != nil {
		return err
	}
	_, err = s.adas.Call("add", " ", s.ctx)
	_, _ = fmt.Fprintln(s.out, "   ada adds ' ':", describe(err))
	return nil
}

// exportStep is chapter X: export and import. The list's running worker writes its own store and files as one archive and goes on;
// importing the archive makes a new list with the same todos. An address that has data is never overwritten.
func (s *session) exportStep() error {
	address := func(list string) delta.JsonValue { return delta.JsonObjectOf("id", list) }
	archive := filepath.Join(s.base, "groceries.tar")
	if err := s.app.ExportData(listKind.Address(address("groceries").(*delta.JsonObject)), archive, s.ctx); err != nil {
		return err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return err
	}
	exported := "nothing"
	if info.Size() > 0 {
		exported = "an archive"
	}
	_, _ = fmt.Fprintln(s.out, "10. exported groceries:", exported)
	if err := s.app.ImportData(archive, listKind.Address(address("picnic").(*delta.JsonObject)), s.ctx); err != nil {
		return err
	}
	err = s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		picnic, err := ctx.Get(todos, delta.JsonObjectOf("list", "picnic"), host.GetOptions{})
		if err != nil {
			return err
		}
		items, err := itemsUntil(picnic, func(items []todo) bool { return len(items) > 0 })
		_, _ = fmt.Fprintln(s.out, "   imported as picnic:", show(items))
		return err
	})
	if err != nil {
		return err
	}
	err = s.app.ImportData(archive, listKind.Address(address("groceries").(*delta.JsonObject)), s.ctx)
	if err != nil {
		_, _ = fmt.Fprintln(s.out, "   importing over groceries:", strings.ReplaceAll(err.Error(), s.base, "…"))
	}
	return nil
}
