package codingagent

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// OSC 8 hyperlinks end in BEL or ST: Pi 1.0.1's sign-in URLs use pi-tui hyperlink(), which ends in ST.
var llamaTestANSI = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x07\x1b]*(?:\x07|\x1b\\)|\x1b_[^\x07]*\x07`)

func plainRender(component tui.Component) string {
	return llamaTestANSI.ReplaceAllString(strings.Join(component.Render(400), "\n"), "")
}

func waitForRender(t *testing.T, component tui.Component, want string) {
	t.Helper()
	// Windows CI runners stall for seconds under load; the wait ends as soon as the text renders.
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(plainRender(component), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never rendered %q; last render:\n%s", want, plainRender(component))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
