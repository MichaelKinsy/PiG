//go:build unix

package cellpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
)

// copyTree copies a folder, so a link can stand in for it with the same content.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	if out, err := exec.CommandContext(context.Background(), "cp", "-R", from, to).CombinedOutput(); err != nil {
		t.Fatalf("cp -R %s %s: %v\n%s", from, to, err, out)
	}
}

// Links and modes are what an attacker with the user's rights changes without touching a byte of a file: a link that points at an identical copy kept by someone else, a mode that lets another user write, an executable bit.
func TestNodeCellStartExtractsTheArchiveAgainForLinksAndModes(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, f *nodeFixture){
		"a file replaced by a link to a file with the same bytes": func(t *testing.T, f *nodeFixture) {
			outside := filepath.Join(t.TempDir(), "index.ts")
			writeAll(t, outside, "export default function () {}\n")
			if err := os.Remove(f.path("greeter/index.ts")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, f.path("greeter/index.ts")); err != nil {
				t.Fatal(err)
			}
		},
		"a file replaced by a link to other bytes": func(t *testing.T, f *nodeFixture) {
			outside := filepath.Join(t.TempDir(), "index.ts")
			writeAll(t, outside, "evil")
			if err := os.Remove(f.path("greeter/index.ts")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, f.path("greeter/index.ts")); err != nil {
				t.Fatal(err)
			}
		},
		"a member folder replaced by a link to a copy": func(t *testing.T, f *nodeFixture) {
			copyDir := filepath.Join(t.TempDir(), "lib")
			copyTree(t, f.path("greeter/lib"), copyDir)
			if err := os.RemoveAll(f.path("greeter/lib")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(copyDir, f.path("greeter/lib")); err != nil {
				t.Fatal(err)
			}
		},
		"the sources folder replaced by a link to a copy": func(t *testing.T, f *nodeFixture) {
			copyDir := filepath.Join(t.TempDir(), "sources")
			copyTree(t, f.root, copyDir)
			if err := os.RemoveAll(f.root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(copyDir, f.root); err != nil {
				t.Fatal(err)
			}
		},
		"the digest folder replaced by a link to a copy": func(t *testing.T, f *nodeFixture) {
			copyDir := filepath.Join(t.TempDir(), "entry")
			copyTree(t, f.entryDir, copyDir)
			if err := os.RemoveAll(f.entryDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(copyDir, f.entryDir); err != nil {
				t.Fatal(err)
			}
		},
		"a link given another target": func(t *testing.T, f *nodeFixture) {
			name := f.path("greeter/node_modules/.bin/dep")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../index.ts", name); err != nil {
				t.Fatal(err)
			}
		},
		"a link replaced by a file": func(t *testing.T, f *nodeFixture) {
			name := f.path("greeter/node_modules/.bin/dep")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			writeAll(t, name, "module.exports = 1;\n")
		},
		"a file another user can write": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/index.ts"), 0o666); err != nil {
				t.Fatal(err)
			}
		},
		"a file its group can write": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/lib/echo.ts"), 0o664); err != nil {
				t.Fatal(err)
			}
		},
		"a folder another user can write": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/lib"), 0o777); err != nil {
				t.Fatal(err)
			}
		},
		"the sources folder another user can write": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.root, 0o777); err != nil {
				t.Fatal(err)
			}
		},
		"a setuid file": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/bin/run"), 0o755|os.ModeSetuid); err != nil {
				t.Fatal(err)
			}
		},
		"the executable bit dropped": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/bin/run"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"the executable bit added": func(t *testing.T, f *nodeFixture) {
			if err := os.Chmod(f.path("greeter/index.ts"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNodeFixture(t, true)
			tamper(t, f)
			if err := f.start(); err != nil {
				t.Fatalf("start after the change: %v", err)
			}
			f.requireArchive(t)
			if left := f.leftovers(t); len(left) != 0 {
				t.Errorf("the start left %v beside the entry", left)
			}
		})
	}
}

// A user's umask narrows what extraction writes; that is not a difference, or every start would extract again.
func TestNodeCellStartAcceptsTheModesAStrictUmaskLeaves(t *testing.T) {
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })
	f := newNodeFixture(t, true)
	before := snapshotTree(t, f.entryDir)
	if err := f.start(); err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, f.entryDir); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("a start under umask 077 replaced a clean tree:\nbefore %v\nafter  %v", before, after)
	}
}

const (
	envNodeHelper = "CELLPACK_NODE_HELPER"
	envNodeDest   = "CELLPACK_NODE_DEST"
	envNodeResult = "CELLPACK_NODE_RESULT"
)

// TestNodeCellStartHelperProcess is not a test: re-run with CELLPACK_NODE_HELPER it is one Binary start in its own process.
func TestNodeCellStartHelperProcess(t *testing.T) {
	if os.Getenv(envNodeHelper) != "1" {
		t.Skip("helper process entry point")
	}
	data, digest := nodeArchive(t, nodeFixtureEntries(true)...)
	fsys := fstest.MapFS{"cells/node/abc.tar": {Data: data}}
	result := "ok"
	if err := extract(fsys, nodeManifest(digest), os.Getenv(envNodeDest), io.Discard); err != nil {
		result = "ERR:" + err.Error()
	}
	if err := os.WriteFile(os.Getenv(envNodeResult), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Processes starting at once on one digest folder, each on its own: a tampered tree is replaced once and every process ends with the complete tree; a clean tree is not touched by any of them. No process deletes a folder another is using.
func TestNodeCellStartsAtOnceOnATamperedAndOnACleanTree(t *testing.T) {
	const processes = 8
	run := func(t *testing.T, f *nodeFixture) {
		t.Helper()
		var wg sync.WaitGroup
		results := make([]string, processes)
		for i := range processes {
			wg.Go(func() {
				result := filepath.Join(t.TempDir(), "result")
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestNodeCellStartHelperProcess")
				cmd.Env = append(os.Environ(), envNodeHelper+"=1", envNodeDest+"="+f.dest, envNodeResult+"="+result)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("process %d: %v\n%s", i, err, out)
					return
				}
				body, err := os.ReadFile(result)
				if err != nil {
					t.Errorf("process %d wrote no result: %v", i, err)
					return
				}
				results[i] = string(body)
			})
		}
		wg.Wait()
		for i, result := range results {
			if result != "ok" {
				t.Errorf("process %d: %s", i, result)
			}
		}
		f.requireArchive(t)
		if left := f.leftovers(t); len(left) != 0 {
			t.Errorf("leftovers %v", left)
		}
	}
	t.Run("tampered", func(t *testing.T) {
		f := newNodeFixture(t, true)
		writeAll(t, f.path("greeter/index.ts"), "export default function () { EVIL }\n")
		writeAll(t, f.path("greeter/node_modules/shadow/index.js"), "evil")
		if err := os.Remove(f.path("greeter/lib/echo.ts")); err != nil {
			t.Fatal(err)
		}
		run(t, f)
	})
	t.Run("clean", func(t *testing.T) {
		f := newNodeFixture(t, true)
		before := snapshotTree(t, f.entryDir)
		run(t, f)
		if after := snapshotTree(t, f.entryDir); fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatalf("starts at once changed a clean tree:\nbefore %v\nafter  %v", before, after)
		}
	})
}

// The refusal for a name above the entry must not depend on the entry: it holds with a clean tree and with a tampered one.
func TestNodeCellStartRefusesBeforeTouchingATamperedTree(t *testing.T) {
	f := newNodeFixture(t, true)
	writeAll(t, f.path("greeter/index.ts"), "export default function () { EVIL }\n")
	forbidden := filepath.Join(f.dest, "node", "node_modules")
	writeAll(t, forbidden, "x")
	err := f.start()
	if err == nil || !strings.Contains(err.Error(), forbidden) {
		t.Fatalf("start = %v, want a refusal naming %s", err, forbidden)
	}
}

// A backslash separates folders on Windows, where no file name holds one, and is an ordinary character of a name elsewhere. The check joins the archive's names, slash paths on every system, with the base names a listing returns and never splits those: an entry with a backslash in its name that the archive does not name is an added entry, reported whole, in a folder the archive does name.
func TestNodeVerifyReportsAnAddedNameWithABackslashWhole(t *testing.T) {
	f := newNodeFixture(t, false)
	if err := verifyNodeEntry(bytes.NewReader(f.data), f.entryDir); err != nil {
		t.Fatalf("a clean tree differs: %v", err)
	}
	writeAll(t, f.path(`greeter/lib/a\b.ts`), "x")
	var difference *nodeDifference
	err := verifyNodeEntry(bytes.NewReader(f.data), f.entryDir)
	if !errors.As(err, &difference) || difference.what != "added" || difference.name != `sources/greeter/lib/a\b.ts` {
		t.Fatalf("verifyNodeEntry = %v, want added: sources/greeter/lib/a\\b.ts", err)
	}
}
