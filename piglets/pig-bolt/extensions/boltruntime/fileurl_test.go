package boltruntime

import "testing"

// The expectations are what Node 24 and Bun 1.4 print for pathToFileURL of each path (checked in the CI container: node and the Pi-Bolt runtime agree on every one).
func TestFileURLEncodesAsNodeDoes(t *testing.T) {
	for path, want := range map[string]string{
		"/a/b c/d.ts":          "file:///a/b%20c/d.ts",
		"/a/b%c":               "file:///a/b%25c",
		"/a/b#c?d":             "file:///a/b%23c%3Fd",
		"/a/b^c":               "file:///a/b%5Ec",
		"/a/b`c{d}e|f":         "file:///a/b%60c%7Bd%7De%7Cf",
		"/a/ü/é.ts":            "file:///a/%C3%BC/%C3%A9.ts",
		"/a/😀/x":               "file:///a/%F0%9F%98%80/x",
		"/a/b[c]!*(x)'y":       "file:///a/b%5Bc%5D!*(x)'y",
		"/a/b\\c":              "file:///a/b%5Cc",
		"/a/<>\"x":             "file:///a/%3C%3E%22x",
		"/a/\u00a0z":           "file:///a/%C2%A0z",
		"/a/~tilde":            "file:///a/%7Etilde",
		"/a/b;c=d&e+f,g$h@i:j": "file:///a/b;c=d&e+f,g$h@i:j",
		"/a/b\nc\td":           "file:///a/b%0Ac%09d",
	} {
		if got := fileURL(path); got != want {
			t.Errorf("fileURL(%q) = %q, want %q", path, got, want)
		}
	}
}
