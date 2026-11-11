package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A state push encodes the system prompt options once per key the host reports, and again when the key changes; a host that reports no key, or a nil one, has them encoded every time.
func TestSnapshotEncodesSystemPromptOptionsOncePerKey(t *testing.T) {
	bridge := NewUIBridge(func() {})
	reads := 0
	cwd := "/one"
	var key any = 1
	actions := &HostCallbacks{GetSystemPromptOptions: func() extension.BuildSystemPromptOptions {
		reads++
		return extension.BuildSystemPromptOptions{Cwd: cwd}
	}}
	bridge.SetActions(actions)
	encoded := func() string { return string(bridge.Snapshot(nil, 0, false).SystemPromptOptions) }
	cwdOf := func(raw string) string {
		var options extension.BuildSystemPromptOptions
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			t.Fatal(err)
		}
		return options.Cwd
	}

	if got := cwdOf(encoded()); got != "/one" || reads != 1 {
		t.Fatalf("without a key: cwd %q after %d reads", got, reads)
	}
	if got := cwdOf(encoded()); got != "/one" || reads != 2 {
		t.Fatalf("without a key the options are read every push: cwd %q after %d reads", got, reads)
	}

	bridge.SetHostAction("getSystemPromptOptionsKey", func() any { return key })
	first := encoded()
	reads = 0
	cwd = "/changed-without-a-new-key"
	if got := encoded(); got != first || reads != 0 {
		t.Fatalf("an unchanged key re-read the options: %s after %d reads", got, reads)
	}
	key, cwd = 2, "/two"
	if got := cwdOf(encoded()); got != "/two" {
		t.Fatalf("a new key kept the old encoding: cwd %q", got)
	}
	key = nil
	reads = 0
	cwd = "/three"
	if got := cwdOf(encoded()); got != "/three" || reads == 0 {
		t.Fatalf("a nil key must not keep an encoding: cwd %q after %d reads", got, reads)
	}
}
