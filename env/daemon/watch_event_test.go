package daemon

// pi: packages/env/daemon/src/watch.rs

import (
	"encoding/json"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// watch.rs sends the change events of a watch as: paths (watch.rs:665), overflow with the polling mode, which only switch_to_polling sends (watch.rs:583), and error (watch.rs:671). Before, an overflow sent while the watch opened (the switch happens in its first sync) had no mode, because the watcher was not stored yet.
func TestWatchEventsMatchTheRustDaemon(t *testing.T) {
	for _, test := range []struct {
		change durableenv.WatchChange
		want   string
	}{
		{durableenv.WatchChangePaths{Paths: []string{"/a", "/b"}}, `{"kind":"change","paths":["/a","/b"]}`},
		{durableenv.WatchChangeOverflow{}, `{"kind":"change","mode":"polling","overflow":true}`},
		{durableenv.WatchChangeError{Error: durableenv.NewFileError(durableenv.FileErrorPermissionDenied, "denied", "", nil)}, `{"code":"permission_denied","kind":"error","message":"denied"}`},
	} {
		encoded, err := json.Marshal(watchEvent(test.change))
		if err != nil || string(encoded) != test.want {
			t.Errorf("%T: %s, %v; want %s", test.change, encoded, err, test.want)
		}
	}
}
