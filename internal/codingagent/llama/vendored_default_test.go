package llama

// pi: packages/coding-agent/src/extensions/llama/provider.ts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Node extension runtime registers Pi's own llama.cpp built-in extension from the vendored Pi
// JavaScript, so its default server URL must be the one the Go provider uses (Pi b3c2e4a6bef8, #12001).
func TestVendoredPiDistDefaultServerURLMatchesGo(t *testing.T) {
	shims := filepath.Join("..", "..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pi-dist", "pi-coding-agent")
	want := `DEFAULT_LLAMA_SERVER_URL = "` + DefaultLlamaServerURL + `";`
	for _, file := range []string{
		filepath.Join(shims, "extensions", "llama", "provider.js"),
		filepath.Join(shims, "sdk-bundle", "index.js"),
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not declare %s", file, want)
		}
	}
}
