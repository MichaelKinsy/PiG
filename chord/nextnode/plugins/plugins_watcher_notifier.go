package plugins

import "github.com/fsnotify/fsnotify"

// notifier is the system's file watcher as the plugin watcher uses it: fsnotify's `Watcher`, one watch per directory, or a
// test's stand-in for another system's (the notifier a watcherOptions names).
type notifier interface {
	Add(name string) error
	Remove(name string) error
	WatchList() []string
	// Close ends the watches and closes both channels.
	Close() error
	// Events and Errors are what the one goroutine the watcher owns reads.
	Events() <-chan fsnotify.Event
	Errors() <-chan error
	// PartialWatches reports whether a watch in WatchList may see less than the Add that put it asked for. kqueue's does: the
	// notifier holds each entry of a watched directory for its removal or rename alone, and a directory it let go of when it was
	// renamed is held that way again when it is back, with no event for what is made in it, until it is added again. Where the
	// watch of a path follows the path (Pi's libuv uses FSEvents on macOS), nothing is lost that way.
	PartialWatches() bool
}

// systemNotifier is fsnotify's watcher of the system.
type systemNotifier struct{ *fsnotify.Watcher }

func (n systemNotifier) Events() <-chan fsnotify.Event { return n.Watcher.Events }
func (n systemNotifier) Errors() <-chan error          { return n.Watcher.Errors }
func (systemNotifier) PartialWatches() bool            { return systemPartialWatches }

// newSystemNotifier is fsnotify's watcher.
func newSystemNotifier() (notifier, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return systemNotifier{watcher}, nil
}
