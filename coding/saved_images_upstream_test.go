package coding

import (
	"encoding/base64"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The saved-image helpers are shared by the codemode and MCP session ports, so they carry neither strip tag: a build
// that strips one of the two built-ins still compiles the other's tests (D92).

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

var tinyPNGLabel = regexp.MustCompile(`^\[Image saved to (\S+\.png) \(image/png, \d+B\)\]$`)

// checkSavedImages is upstream's checkSavedImages: it replaces the `[Image saved to ...]` labels in text with `<saved>`
// after checking that each file holds the tiny PNG, and removes the files.
func checkSavedImages(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		match := tinyPNGLabel.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		data, err := os.ReadFile(match[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := base64.StdEncoding.EncodeToString(data); got != tinyPNGBase64 {
			t.Errorf("saved image %s = %q, want the tiny PNG", match[1], got)
		}
		if err := os.Remove(match[1]); err != nil {
			t.Fatal(err)
		}
		lines[i] = "<saved>"
	}
	return strings.Join(lines, "\n")
}
