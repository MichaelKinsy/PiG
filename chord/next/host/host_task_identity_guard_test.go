package host

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Go-only (no Pi case): the guard of the identity of a task. In Pi a task is an async frame, or the object that holds it, and no
// thread id stands for it (host/object.ts:1625-1633, host.ts:1690-1696). A Go task is a goroutine that a section hands from
// goroutine to goroutine: the one it started on, and the coroutine of each handler adopted for it (parking.Adopt) while its own
// waits for the coroutine. An index of tasks by goroutine ID, or a task's ID remembered, is stale after the first adoption: the
// pacer's was deleted by an ID that Adopt had rewritten and kept an ended task and the attempt its work closes over, and found
// nothing for a send from the coroutine, which then was not marked as a transport wait.
//
// So the registry parking keeps (Task.Enter, Adopt, Current, Task.Goroutines) is the only place a goroutine's ID leads to a
// task, and production code under chord/next and in parking reads a goroutine's ID, or declares a map keyed by one, at the sites
// below only, each of which says what it decides and why no task is found by it. A task is found with parking.Current, and what
// runs a task is asked of Task.Goroutines.

// readsGoroutineID is a line of production code that reads the ID of a goroutine (goroutineid.Current, the parse of a stack
// header, a method named GID) or declares a map keyed by a uint64, the type of an ID.
var readsGoroutineID = regexp.MustCompile(`goroutineid\.Current\(|\.GID\(\)|runtime\.Stack\(|map\[uint64\]`)

// goroutineIDSite is a line of production code that reads a goroutine's ID, and why that finds no task.
type goroutineIDSite struct {
	// file is relative to the chord directory; code is the line with its comment cut and its spaces collapsed; count how many such
	// lines the file has.
	file, code string
	count      int
	reason     string
}

var goroutineIDSites = []goroutineIDSite{
	{
		file: "next/host/host_loop.go", code: "func (l *Loop) onLoop() bool { return l.owner.Load() == goroutineid.Current() }", count: 1,
		reason: "the section's owner: whether the caller is the goroutine the loop's section runs on, which Lend hands to a handler's coroutine and Signal.Raise hands back; it finds no task",
	},
	{
		file: "next/host/host_loop.go", code: "id := goroutineid.Current()", count: 1,
		reason: "Run: the same comparison with Loop.owner, then the caller becomes the owner of the section it runs",
	},
	{
		file: "next/host/host_loop_lend.go", code: "id := goroutineid.Current()", count: 1,
		reason: "lendRun.exec on the coroutine: its own ID, bound to its call's signal (only a Raise on it gives the section back) and stored as the loop's owner while the section is lent; Loop.finish passes it to parking.Adopt, which registers it for the task",
	},
	{
		file: "next/host/host_loop_lend.go", code: "section := goroutineid.Current()", count: 1,
		reason: "Lend: the section's goroutine, which the owner is lent from and handed back to; it finds no task",
	},
	{
		file: "next/host/host_loop_seam.go", code: "dump := string(stacks[:runtime.Stack(stacks, true)])", count: 1,
		reason: "stalled: the dump of every goroutine's stack that the report picks blocks of: the goroutines of a task are asked of Task.Goroutines, the section's owner of Loop.owner",
	},
	{
		file: "next/tui/tui_scope.go", code: "active map[uint64]int", count: 1,
		reason: "TuiScope's record of the goroutines that run a `changed`, so that Dispose waits for a delivery on another goroutine: Go-only, Pi's dispose is a flag of one thread (tui/scope.ts); no task, no parking",
	},
	{
		file: "next/tui/tui_scope.go", code: "disposing map[uint64]bool", count: 1,
		reason: "TuiScope: the goroutines that are in Dispose, so that two `changed` that dispose their scope do not wait for each other; see active",
	},
	{
		file: "next/tui/tui_scope.go", code: "s := &TuiScope{host: h, changed: changed, ctx: ctx, cancel: cancel, active: map[uint64]int{}, disposing: map[uint64]bool{}}", count: 1,
		reason: "TuiScope's two records, made; see active",
	},
	{
		file: "next/tui/tui_scope.go", code: "line := buf[:runtime.Stack(buf[:], false)]", count: 1,
		reason: "TuiScope's goroutineID: the number in the first line of the caller's stack; see active",
	},
	{
		file: "internal/parking/task.go", code: "id := goroutineid.Current()", count: 1,
		reason: "Task.Enter: the registry entry of the goroutine a task starts on, deleted by this captured ID when the task leaves",
	},
	{
		file: "internal/parking/task.go", code: "found, ok := tasks.Load(goroutineid.Current())", count: 1,
		reason: "Current: THE lookup of a task by the goroutine that runs it, in the registry Enter and Adopt keep; every other lookup asks it",
	},
	{
		file: "internal/parking/parking.go", code: "if s.yield != nil && goroutineid.Current() != s.gid {", count: 1,
		reason: "Signal.raise: only a Raise on the coroutine's goroutine, bound once on it, gives the section back; a goroutine the handler started cannot; it finds no task",
	},
	{
		file: "internal/parking/stall.go", code: "stacks = stacks[:runtime.Stack(stacks, true)]", count: 1,
		reason: "the report of a section that a handler holds: the stack of every goroutine; it finds no task",
	},
}

func TestProductionReadsAGoroutineIDAtTheListedSitesOnlyNoTaskIsFoundByIt(t *testing.T) {
	chordDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, root := range []string{filepath.Join(chordDir, "next"), filepath.Join(chordDir, "internal", "parking")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file, err := filepath.Rel(chordDir, path)
			if err != nil {
				return err
			}
			for line := range strings.SplitSeq(string(source), "\n") {
				code := strings.Join(strings.Fields(withoutComment(line)), " ")
				if code != "" && readsGoroutineID.MatchString(code) {
					found[filepath.ToSlash(file)+"\x00"+code]++
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	listed := map[string]goroutineIDSite{}
	for _, site := range goroutineIDSites {
		if site.reason == "" {
			t.Errorf("%s: `%s` is listed without its reason", site.file, site.code)
		}
		listed[site.file+"\x00"+site.code] = site
	}
	for key, n := range found {
		file, code, _ := strings.Cut(key, "\x00")
		site, ok := listed[key]
		switch {
		case !ok:
			t.Errorf("%s: `%s` reads the ID of a goroutine: a task is not one goroutine (a handler's coroutine is adopted for it), so no task is found, "+
				"marked, counted or deleted by an ID: ask parking.Current for the task the caller runs and Task.Goroutines for what runs a task; "+
				"a read that finds no task is listed in goroutineIDSites with its reason", file, code)
		case n != site.count:
			t.Errorf("%s: `%s` appears %d times, goroutineIDSites lists %d", file, code, n, site.count)
		}
	}
	for key, site := range listed {
		if found[key] == 0 {
			t.Errorf("%s: `%s` is listed in goroutineIDSites and no longer there: remove the entry", site.file, site.code)
		}
	}
}
