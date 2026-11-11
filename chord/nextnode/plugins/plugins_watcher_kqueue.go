//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package plugins

// systemPartialWatches is whether the system's notifier may hold a watch that sees less than it was added for: fsnotify's kqueue
// (the systems it is built on) watches a directory's entries for their removal or rename alone, and does that for a directory
// it let go of at its rename when the directory is back (see notifier.PartialWatches).
const systemPartialWatches = true
