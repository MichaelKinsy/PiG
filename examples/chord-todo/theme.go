package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	nextsurfaces "github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// themeStep is chapter VI: a theme plugin added while the app runs: a new directory in the plugins root marks the set changed, and
// applying the changes builds it and rolls it out. Pages and terminals restyle in place.
func (s *session) themeStep() error {
	if err := os.CopyFS(filepath.Join(s.root, "harbor"), os.DirFS(filepath.Join(here, "later", "harbor"))); err != nil {
		return err
	}
	if err := until(func() (bool, error) { return s.app.Plugins.Marks().Set, nil }); err != nil {
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
	fmt.Fprintln(s.out, "6. harbor added, rolled out to:", strings.Join(steps, ", "))
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
	fmt.Fprintln(s.out, "   the page's plugins:", strings.Join(names, ", "))
	if s.terminal != nil {
		const accent = "\x1b[38;2;242;166;90mGroceries"
		if err := until(func() (bool, error) { return strings.Contains(s.terminal.frames(), accent), nil }); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "   the terminal redraws its title in Harbor's accent: %q\n", accent)
	}
	return nil
}
