//go:build !unix

package cellpack

import (
	"fmt"
	"os"
)

// sizeAndIdentity names a file's size; the modification time in a snapshot tells a rewritten file from an untouched one.
func sizeAndIdentity(info os.FileInfo) string { return fmt.Sprint(info.Size()) }
