//go:build !linux && !darwin

package viewendpoint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

type otherHost struct{ reg *uiview.Registry }

func (h *otherHost) Views() *uiview.Registry { return h.reg }
func (h *otherHost) InvokeViewIntentFrom(context.Context, *uiview.Attachment, string, uiview.Ref, string, string, json.RawMessage) uiview.Result {
	return uiview.Result{}
}

// pig additive (D614): where the kernel does not name a peer's user and a socket file keeps no mode, the endpoint
// serves nothing and says so once, however often a host is bound.
func TestNoSocketIsServedOnThisSystem(t *testing.T) {
	var notices []string
	e := New(Options{Dir: t.TempDir(), Notify: func(message string) { notices = append(notices, message) }})
	for range 3 {
		e.Bind(&otherHost{reg: uiview.New(uiview.Options{})})
		e.Unbind()
	}
	e.Close()
	if Supported || e.Path() != "" {
		t.Fatal("a socket is served")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "viewEndpoint: socket is not available on") {
		t.Fatalf("notices = %q, want one that says the key is not available here", notices)
	}
}
