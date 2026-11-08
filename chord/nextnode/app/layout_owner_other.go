//go:build !unix

package app

import "os"

// ownerOf is the user id that owns the file info describes; a platform without user ids (Windows) has none, as Node's
// `stat.uid` is not meaningful there either.
func ownerOf(os.FileInfo) (uid int, ok bool) { return 0, false }
