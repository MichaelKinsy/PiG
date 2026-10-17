package outputfiles

import "strings"

// nodeTmpdir is Node's os.tmpdir() (lib/os.js tmpdir; src/node_os.cc GetTempDir). On Windows it is the first non-empty TEMP or TMP, else
// %SystemRoot% (or %windir%) + "\temp", without one trailing backslash unless it ends a drive root. Elsewhere it is the first non-empty
// TMPDIR, TMP or TEMP, else /tmp, without one trailing slash unless the path is "/".
func nodeTmpdir(goos string, getenv func(string) string) string {
	if goos == "windows" {
		path := getenv("TEMP")
		if path == "" {
			path = getenv("TMP")
		}
		if path == "" {
			root := getenv("SystemRoot")
			if root == "" {
				root = getenv("windir")
			}
			if root == "" {
				// JavaScript concatenates the undefined variable as text.
				root = "undefined"
			}
			path = root + `\temp`
		}
		if len(path) > 1 && strings.HasSuffix(path, `\`) && !strings.HasSuffix(path, `:\`) {
			path = path[:len(path)-1]
		}
		return path
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if path := getenv(name); path != "" {
			if len(path) > 1 && strings.HasSuffix(path, "/") {
				path = path[:len(path)-1]
			}
			return path
		}
	}
	return "/tmp"
}
