//go:build unix

package env

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/env/daemon"
)

// TestWatchWindowHoldsTheSetOfPathsOfAChange: how a change's paths are split into events depends on where a poll falls among its
// operations, so a window holds each path once, however many events reported it, and ends at the sentinel's event.
func TestWatchWindowHoldsTheSetOfPathsOfAChange(t *testing.T) {
	const root = "/r"
	for _, test := range []struct {
		name     string
		touched  []string
		reported []string
		events   [][]string
		want     []string
		// ended is the number of events the window takes before its sentinel arrives.
		ended int
	}{
		{
			name:    "a file seen empty, then written",
			touched: []string{"/r/staging/n2"},
			events:  [][]string{{"/r/staging/n2"}, {"/r/staging/n2", "/r/watched/zz-sentinel-1"}},
			want:    []string{"{ROOT}/staging/n2"},
			ended:   2,
		},
		{
			name:    "a touched file two events reported",
			touched: []string{"/r/watched/a.txt"},
			events:  [][]string{{"/r/watched/a.txt"}, {"/r/watched/a.txt"}, {"/r/watched/zz-sentinel-1"}},
			want:    []string{"{ROOT}/watched/a.txt"},
			ended:   3,
		},
		{
			name: "a rename scanned from one target and then the other",
			events: [][]string{
				{"/r/staging/tree/inner/x2", "/r/staging/tree/x1"},
				{"/r/staging/tree", "/r/watched/tree2", "/r/watched/zz-sentinel-1"},
			},
			want:  []string{"{ROOT}/staging/tree", "{ROOT}/staging/tree/inner/x2", "{ROOT}/staging/tree/x1", "{ROOT}/watched/tree2"},
			ended: 2,
		},
		{
			name:     "the re-report of an earlier window is not this window's",
			reported: []string{"/r/watched/b"},
			events:   [][]string{{"/r/watched/b", "/r/watched/zz-sentinel-0"}, {"/r/watched/c", "/r/watched/zz-sentinel-1"}},
			want:     []string{"{ROOT}/watched/c"},
			ended:    2,
		},
		{
			name:     "a touched path an earlier window reported is this window's again",
			touched:  []string{"/r/watched/b"},
			reported: []string{"/r/watched/b"},
			events:   [][]string{{"/r/watched/b", "/r/watched/zz-sentinel-1"}},
			want:     []string{"{ROOT}/watched/b"},
			ended:    1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			toSet := func(names []string) map[string]bool {
				set := map[string]bool{}
				for _, name := range names {
					set[name] = true
				}
				return set
			}
			window := newWatchWindow("/r/watched/zz-sentinel-1", toSet(test.touched), toSet(test.reported))
			taken := 0
			for _, event := range test.events {
				taken++
				if window.add(event) {
					break
				}
			}
			if taken != test.ended {
				t.Fatalf("the window ended after %d events, want %d", taken, test.ended)
			}
			if got := window.paths(root); !slices.Equal(got, test.want) {
				t.Fatalf("window = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInJavaScriptOrderIsUTF16CodeUnitOrder(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths []string
		want  bool
	}{
		{"no paths", nil, true},
		{"one path", []string{"/r/a"}, true},
		{"sorted", []string{"/r/a", "/r/a/b", "/r/b"}, true},
		{"a prefix first", []string{"/r/staging/tree", "/r/staging/tree/inner"}, true},
		{"a child before its directory", []string{"/r/staging/tree/inner", "/r/staging/tree"}, false},
		{"two events joined", []string{"/r/staging/tree/inner/x2", "/r/staging/tree", "/r/staging/tree/inner"}, false},
		// U+1F600 is the surrogate pair D83D DE00 and U+FF5E one unit: UTF-16 puts the astral character first, UTF-8 bytes last.
		{"an astral character before U+FF5E", []string{"/r/\U0001F600", "/r/\uFF5E"}, true},
		{"U+FF5E before an astral character", []string{"/r/\uFF5E", "/r/\U0001F600"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := inJavaScriptOrder(test.paths); got != test.want {
				t.Fatalf("inJavaScriptOrder(%q) = %v, want %v", test.paths, got, test.want)
			}
		})
	}
}

// TestDaemonWatchReportsAHalfWrittenFileTwiceLikePisDaemon makes the poll that falls between the creation of a file and the write of
// its content, which a stalled test leaves to chance, happen on purpose: the content is written only after the daemon reported the
// empty file, so no timing is involved. Both daemons report the path again after the write, and the window the watch test compares
// holds it once.
func TestDaemonWatchReportsAHalfWrittenFileTwiceLikePisDaemon(t *testing.T) {
	request := object("op", "watch", "targets", []any{object("path", "{ROOT}/watched")}, "mode", "polling", "pollIntervalMs", 40)
	const want = "reported again after the write: true\nwindow: [\"{ROOT}/watched/half\"]\n"
	pi, pig := runOnBoth(t, watchTree, func(d *wireDaemon, root string) string {
		id := d.start(withRoot(request, root).(daemon.Object), nil)
		if ready := d.next(); ready.Kind != daemon.FrameEvent || ready.JSON.(daemon.Object)["kind"] != "ready" {
			t.Fatalf("the watch did not start: %+v", ready)
		}
		file := filepath.Join(root, "watched", "half")
		sentinel := filepath.Join(root, "watched", "zz-sentinel")
		window := newWatchWindow(sentinel, map[string]bool{file: true}, map[string]bool{})
		named := 0
		// take adds the next change event to the window and reports whether it held the sentinel.
		take := func() bool {
			frame := d.next()
			if frame.ID != id || frame.Kind != daemon.FrameEvent || frame.JSON.(daemon.Object)["kind"] != "change" {
				t.Fatalf("unexpected frame %+v", frame)
			}
			var batch []string
			for _, path := range frame.JSON.(daemon.Object)["paths"].([]any) {
				batch = append(batch, path.(string))
			}
			if slices.Contains(batch, file) {
				named++
			}
			return window.add(batch)
		}
		mustDo(os.WriteFile(file, nil, 0o644))
		for named == 0 {
			take()
		}
		// One write without truncating, so a scan cannot see the file between two operations again.
		handle, err := os.OpenFile(file, os.O_WRONLY, 0)
		mustDo(err)
		_, err = handle.WriteString("content")
		mustDo(err)
		mustDo(handle.Close())
		mustDo(os.WriteFile(sentinel, nil, 0o644))
		for !take() {
		}
		return fmt.Sprintf("reported again after the write: %v\nwindow: %q\n", named >= 2, window.paths(root))
	})
	compareWire(t, "half written file", pi, pig)
	if pig != want {
		t.Fatalf("both daemons reported %q, want %q", pig, want)
	}
}
