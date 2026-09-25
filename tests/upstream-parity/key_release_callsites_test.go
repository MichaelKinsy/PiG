package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawKeyReleaseCallers lists every production file permitted to call
// tui.IsKeyRelease directly, with the reason its contract is raw rather than
// focused-component delivery.
//
// Focused-component delivery must go through tui.ShouldDeliverKey, which
// applies upstream's rule once (tui.ts:887: drop releases unless the component
// sets wantsKeyRelease). Scattering the check is what produced a recurring
// class of double-input bugs: the check existed, but each newly added dispatch
// path bypassed it, most recently making extension dialogs move a selector
// cursor two rows per arrow press.
//
// Adding a file here is a deliberate assertion that it consumes raw input.
// If it hands data to a component's HandleInput, use ShouldDeliverKey instead.
var rawKeyReleaseCallers = map[string]string{
	"tui/keys_decode.go":                          "defines IsKeyRelease and ShouldDeliverKey",
	"tui/tui_alt_screen_input.go":                 "viewport scrolling consumes the release to stop fallthrough but acts only on press, mirroring upstream handleViewportInput",
	"internal/codingagent/interactive_helpers.go": "extension shortcut listeners see raw input, mirroring upstream addInputListener",
}

// TestKeyReleaseChokePoint fails when a new production file calls
// tui.IsKeyRelease directly. It protects the single delivery rule rather than
// any one call site.
func TestKeyReleaseChokePoint(t *testing.T) {
	root := repoRootForKeyRelease(t)

	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".upstream", ".git", "testdata", "node_modules", "target", "parity":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(src), "IsKeyRelease(") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		found[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(found) == 0 {
		t.Fatal("no IsKeyRelease callers found at all; the scan is broken, not the tree")
	}

	for file := range found {
		if _, ok := rawKeyReleaseCallers[file]; !ok {
			t.Errorf("%s calls tui.IsKeyRelease directly.\n"+
				"Focused-component delivery must use tui.ShouldDeliverKey so key releases are\n"+
				"dropped unless the component opts in via tui.KeyReleaseReceiver. If this file\n"+
				"really consumes raw input, add it to rawKeyReleaseCallers with the reason.", file)
		}
	}
	for file, reason := range rawKeyReleaseCallers {
		if !found[file] {
			t.Errorf("rawKeyReleaseCallers lists %s (%q) but it no longer calls IsKeyRelease; remove the stale entry", file, reason)
		}
	}
}

func repoRootForKeyRelease(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root")
		}
		dir = parent
	}
}

// rawInputLoops lists production files that read terminal input and hand it to
// something other than a focused component, so the delivery filter must not
// apply to them.
var rawInputLoops = map[string]string{
	"tui/terminal.go": "implements ReadInput itself; it has no component to filter for",
}

// TestInputDispatchFiltersKeyReleases fails when production code bypasses the
// process StdinBuffer or hands a decoded sequence to a focused component without
// applying the upstream key-release rule.
func TestInputDispatchFiltersKeyReleases(t *testing.T) {
	root := repoRootForKeyRelease(t)

	filters := []string{"dropKeyReleases(", "ShouldDeliverKey("}
	hasFilter := func(s string) bool {
		for _, f := range filters {
			if strings.Contains(s, f) {
				return true
			}
		}
		return false
	}

	type finding struct {
		file, line, text string
	}
	var unfiltered []finding
	readLoops := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".upstream", ".git", "testdata", "node_modules", "target", "parity":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		file := filepath.ToSlash(rel)
		text := string(src)

		// Raw-read splitting is forbidden: upstream has one stateful
		// StdinBuffer before focus dispatch, which preserves read boundaries.
		if strings.Contains(text, "splitInputChunks(") {
			unfiltered = append(unfiltered, finding{file, "-", "uses the removed stateless input splitter"})
		}

		// A loop that reads terminal input must use the stateful decoder and
		// apply focused-component release filtering before HandleInput.
		if _, raw := rawInputLoops[file]; !raw && strings.Contains(text, "ReadInput(") {
			readLoops++
			if !strings.Contains(text, "ProcessTerminalBytes(") || !hasFilter(text) {
				unfiltered = append(unfiltered, finding{file, "-", "reads terminal input without stateful decoding and key-release filtering"})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if readLoops == 0 {
		t.Fatal("scan found no terminal read loops; the scan is broken, not the tree")
	}

	for _, f := range unfiltered {
		t.Errorf("%s:%s bypasses stateful terminal decoding or focused delivery.\n"+
			"  %s\n"+
			"Every process input stream must pass through StdinBuffer before focus routing,\n"+
			"then use tui.ShouldDeliverKey for the focused component.", f.file, f.line, f.text)
	}
	for file, reason := range rawInputLoops {
		if _, statErr := os.Stat(filepath.Join(root, file)); statErr != nil {
			t.Errorf("rawInputLoops lists %s (%q) but the file is gone; remove the stale entry", file, reason)
		}
	}
}
