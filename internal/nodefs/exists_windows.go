package nodefs

import "os"

// Exists is fs.existsSync: whether name names an existing file system entry, following symbolic links.
func Exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
