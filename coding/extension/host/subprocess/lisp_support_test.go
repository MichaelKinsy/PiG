package subprocess

import (
	"context"
	"errors"
	"testing"
)

// withLispHooks runs test with the hooks of a program that never called EnableLisp, or of one that did, and restores them.
func withLispHooks(t *testing.T, enabled bool) {
	t.Helper()
	saved := lisp
	t.Cleanup(func() { lisp = saved })
	lisp.stage, lisp.current, lisp.command, lisp.hasSource = nil, nil, nil, nil
	if enabled {
		EnableLisp()
	}
}

// A program that never called EnableLisp reports that it has no Lisp support wherever the host would build, find or start a Lisp image,
// and leaves an extension that is not Lisp alone.
func TestLispStagingNeedsEnableLisp(t *testing.T) {
	withLispHooks(t, false)
	host := &Host{}
	cfg := ExtConfig{Name: "lisp-ext", Source: t.TempDir(), Package: "lisp-ext", Factory: "new-extension", RuntimeKind: "subprocess", RuntimeLanguage: "lisp", EntrypointKind: "factory", Enabled: true}
	cells := PlanCells([]ExtConfig{cfg}, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedLisp {
		t.Fatalf("PlanCells = %+v, want one packed-lisp cell", cells)
	}
	if _, err := host.stageLisp(context.Background(), cells[0], nil, &ReloadCellReport{}); !errors.Is(err, errNoLispSupport) {
		t.Errorf("stageLisp error = %v, want errNoLispSupport", err)
	}
	if _, _, err := lispCurrentEntry(t.TempDir(), cells[0]); !errors.Is(err, errNoLispSupport) {
		t.Errorf("lispCurrentEntry error = %v, want errNoLispSupport", err)
	}
	if cmd, err := host.lispCommand(context.Background(), context.Background(), "lisp", "script.lisp"); cmd != nil || err != nil {
		t.Errorf("lispCommand = %v, %v, want neither", cmd, err)
	}
	if lisp.hasSource != nil {
		t.Errorf("a Lisp source hook is installed without EnableLisp")
	}
}

// EnableLisp installs every hook the host consults.
func TestEnableLispInstallsEveryHook(t *testing.T) {
	withLispHooks(t, true)
	if lisp.stage == nil || lisp.current == nil || lisp.command == nil || lisp.hasSource == nil {
		t.Errorf("EnableLisp left a hook unset: stage %t current %t command %t hasSource %t", lisp.stage != nil, lisp.current != nil, lisp.command != nil, lisp.hasSource != nil)
	}
	dir := t.TempDir()
	if lisp.hasSource(dir) {
		t.Errorf("an empty directory holds Lisp source")
	}
}
