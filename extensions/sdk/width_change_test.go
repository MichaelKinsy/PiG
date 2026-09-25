package sdk

import (
	"encoding/json"
	"sync"
	"testing"
)

// A header/footer/widget is sent to the host as static lines, so unlike
// upstream's component factories it does not follow a resize. OnWidthChange is
// the trigger an extension re-pushes from.
func TestOnWidthChangeDeliversNewWidth(t *testing.T) {
	e := &Extension{}
	ctx := Context{ext: e}

	var mu sync.Mutex
	var seen []int
	var widthAtCall []int
	if _, err := ctx.OnWidthChange(func(c Context, w int) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, w)
		// The handler must observe the updated width, or a re-push would
		// rebuild the lines at the width that just became stale.
		widthAtCall = append(widthAtCall, c.Width())
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	e.handleNotify(widthNotify(t, 100))
	e.handleNotify(widthNotify(t, 42))

	mu.Lock()
	defer mu.Unlock()
	if want := []int{100, 42}; !equalInts(seen, want) {
		t.Errorf("handler saw widths %v, want %v", seen, want)
	}
	if want := []int{100, 42}; !equalInts(widthAtCall, want) {
		t.Errorf("Context.Width during handler = %v, want %v (handler ran before the width was stored)",
			widthAtCall, want)
	}
}

// Unsubscribe must stop delivery and be safe to call twice.
func TestOnWidthChangeUnsubscribeIsIdempotent(t *testing.T) {
	e := &Extension{}
	ctx := Context{ext: e}

	var mu sync.Mutex
	calls := 0
	unsub, err := ctx.OnWidthChange(func(Context, int) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	e.handleNotify(widthNotify(t, 80))
	unsub()
	unsub() // must not panic or double-remove
	e.handleNotify(widthNotify(t, 90))

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("handler ran %d time(s), want 1 (delivery continued after unsubscribe)", calls)
	}
}

// A zero or negative width is not a resize; delivering it would have an
// extension rebuild its lines against a width that cannot be laid out.
func TestOnWidthChangeIgnoresNonPositiveWidth(t *testing.T) {
	e := &Extension{}
	ctx := Context{ext: e}

	called := false
	if _, err := ctx.OnWidthChange(func(Context, int) { called = true }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	e.handleNotify(widthNotify(t, 0))
	if called {
		t.Error("a width_change of 0 was delivered to the handler")
	}
}

func TestOnWidthChangeRejectsNilHandler(t *testing.T) {
	e := &Extension{}
	ctx := Context{ext: e}
	unsub, err := ctx.OnWidthChange(nil)
	if err == nil {
		t.Error("a nil handler was accepted; it would panic on the first resize")
	}
	unsub() // must still be safe to call
}

func widthNotify(t *testing.T, width int) envelope {
	t.Helper()
	args, err := json.Marshal(map[string]int{"width": width})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return envelope{Notify: &notifyMsg{Method: "width_change", Args: args}}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
