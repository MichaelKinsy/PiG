package daemon

// pi: packages/env/daemon/src/watch.rs

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// watch.rs sends the change events of a watch as: paths (watch.rs:665), overflow with the polling mode, which only switch_to_polling sends (watch.rs:583), and error (watch.rs:671). Before, an overflow sent while the watch opened (the switch happens in its first sync) had no mode, because the watcher was not stored yet.
func TestWatchEventsMatchTheRustDaemon(t *testing.T) {
	for _, test := range []struct {
		change durableenv.FileChange
		want   string
	}{
		{durableenv.FileChangePaths{Paths: []string{"/a", "/b"}}, `{"kind":"change","paths":["/a","/b"]}`},
		{durableenv.FileChangeOverflow{}, `{"kind":"change","mode":"polling","overflow":true}`},
		{durableenv.FileChangeFailed{Error: durableenv.NewFileError(durableenv.FileErrorPermissionDenied, "denied", "", nil)}, `{"code":"permission_denied","kind":"error","message":"denied"}`},
	} {
		encoded, err := json.Marshal(watchEvent(test.change))
		if err != nil || string(encoded) != test.want {
			t.Errorf("%T: %s, %v; want %s", test.change, encoded, err, test.want)
		}
	}
}

// Pi main 102d1287 (#10516) packages/env/daemon/src/watch.rs change_event_lists_paths_that_fit.
func TestChangeEventListsPathsThatFit(t *testing.T) {
	encoded, err := json.Marshal(changeEvent([]string{"/a", "/b"}))
	if want := `{"kind":"change","paths":["/a","/b"]}`; err != nil || string(encoded) != want {
		t.Fatalf("%s, %v; want %s", encoded, err, want)
	}
}

// Pi main 102d1287 (#10516) watch.rs change_event_overflows_when_paths_do_not_fit_in_a_frame: a frame that is too large would end
// the connection's output, so a change list that does not fit in one is reported as an overflow, which the client reads as
// "rescan". The event also goes through WriteFrame.
func TestChangeEventOverflowsWhenPathsDoNotFitInAFrame(t *testing.T) {
	path := strings.Repeat("x", 4096)
	paths := make([]string, MaxPayload/len(path)+1)
	for i := range paths {
		paths[i] = path
	}
	event := changeEvent(paths)
	encoded, err := json.Marshal(event)
	if want := `{"kind":"change","overflow":true}`; err != nil || string(encoded) != want {
		t.Fatalf("%s, %v; want %s", encoded, err, want)
	}
	if err := WriteFrame(io.Discard, &Frame{Kind: FrameEvent, ID: 1, JSON: event}); err != nil {
		t.Fatalf("the overflow event does not fit a frame: %v", err)
	}
}

// A list of paths that needs JSON escaping is measured by its encoding, not by its length: 1.5 MiB of control characters is
// 9 MiB of \u00XX escapes, within the frame, and 3 MiB of them is not.
func TestChangeEventMeasuresTheEncodedSize(t *testing.T) {
	fits := changeEvent([]string{strings.Repeat("\x01", 1536*1024)})
	if _, ok := fits["paths"]; !ok {
		t.Fatalf("%v: a list within the frame is reported as overflow", fits["kind"])
	}
	overflows := changeEvent([]string{strings.Repeat("\x01", 3*1024*1024)})
	if overflows["overflow"] != true {
		t.Fatal("a list above the frame limit once encoded is not reported as overflow")
	}
}
