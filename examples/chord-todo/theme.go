package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	nextsurfaces "github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// untilBaseWatched waits until app watches the files its bases were built from (Pi's `await app.baseWatched`). Pi 61-tutorial/main.ts
// does it before step 6 (f2bd84a519): on macOS libuv rebuilds its shared FSEvents stream whenever a watch is added or removed and
// drops events in the gap, and the start's base bookkeeping adds watches in the background, so a plugin directory copied meanwhile
// could go unnoticed and the step wait for a mark that never comes. The app here does the same bookkeeping in the background.
func untilBaseWatched(app interface{ BaseWatched() <-chan struct{} }) error {
	select {
	case <-app.BaseWatched():
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("the base code was not watched")
	}
}

// themeStep is chapter VI: a theme plugin added while the app runs: a new directory in the plugins root marks the set changed, and
// applying the changes builds it and rolls it out. Pages and terminals restyle in place.
func (s *session) themeStep() error {
	if err := untilBaseWatched(s.app); err != nil {
		return err
	}
	if err := os.CopyFS(filepath.Join(s.root, "harbor"), os.DirFS(filepath.Join(here, "later", "harbor"))); err != nil {
		return err
	}
	if err := until(func() (bool, error) { return s.app.Plugins.Component.Marks().Set, nil }); err != nil {
		return err
	}
	added, err := s.app.ApplyChanges(s.ctx)
	if err != nil {
		return err
	}
	var steps []string
	for _, step := range added.Steps {
		steps = append(steps, step.Name)
	}
	_, _ = fmt.Fprintln(s.out, "6. harbor added, rolled out to:", strings.Join(steps, ", "))
	scripts, err := s.app.Host.Get(s.ctx, nextsurfaces.WebScripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer scripts.Release()
	set, err := scripts.Member("set").Value()
	if err != nil {
		return err
	}
	var names []string
	for _, plugin := range nextsurfaces.ScriptSetOf(set).Plugins {
		names = append(names, plugin.Name)
	}
	_, _ = fmt.Fprintln(s.out, "   the page's plugins:", strings.Join(names, ", "))
	if s.terminal != nil {
		const accent = "\x1b[38;2;242;166;90mGroceries"
		if err := until(func() (bool, error) { return strings.Contains(s.terminal.frames(), accent), nil }); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(s.out, "   the terminal redraws its title in Harbor's accent: %q\n", accent)
	}
	return nil
}
