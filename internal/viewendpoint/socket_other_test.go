//go:build !linux && !darwin

package viewendpoint

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
	var mu sync.Mutex
	var notices []string
	e := New(Options{Dir: t.TempDir(), Notify: func(message string) {
		mu.Lock()
		notices = append(notices, message)
		mu.Unlock()
	}})
	for range 3 {
		e.Bind(&otherHost{reg: uiview.New(uiview.Options{})})
		e.Unbind()
	}
	// Close drops ops the worker has not dequeued yet; settle first so the Bind calls run before Close.
	e.mu.Lock()
	for e.working {
		e.idle.Wait()
	}
	e.mu.Unlock()
	e.Close()
	if Supported || e.Path() != "" {
		t.Fatal("a socket is served")
	}
	mu.Lock()
	got := append([]string(nil), notices...)
	mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "viewEndpoint: socket is not available on") {
		t.Fatalf("notices = %q, want one that says the key is not available here", got)
	}
}
