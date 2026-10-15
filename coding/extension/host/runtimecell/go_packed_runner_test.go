package runtimecell

import (
	"strings"
	"testing"
)

// The generated runner tells a member that stopped because its socket closed from a real failure by error identity, not by the text of the message.
func TestGoRunnerClassifiesClosedSocketsByErrorIdentity(t *testing.T) {
	source := renderGoRunner([]GoExtension{{Name: "one", Package: "example.com/one"}})
	if !strings.Contains(source, "errors.Is(err, net.ErrClosed)") || !strings.Contains(source, "\t\"errors\"\n") {
		t.Fatalf("the generated runner does not test net.ErrClosed with errors.Is:\n%s", source)
	}
	if strings.Contains(source, "closed network connection") {
		t.Fatal("the generated runner still matches the error text")
	}
}
