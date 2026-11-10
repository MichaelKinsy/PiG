//go:build !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package plugins

// systemPartialWatches is whether the system's notifier may hold a watch that sees less than it was added for: inotify's and
// ReadDirectoryChangesW's do not (see notifier.PartialWatches).
const systemPartialWatches = false
