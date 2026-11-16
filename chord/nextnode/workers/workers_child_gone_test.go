package workers

import "testing"

// Go-only (no Pi case): a child's `gone` is a Promise that resolves once (workers.ts #exited); the channel that stands for it is
// closed once however many paths publish the exit, and a second never panics.
func TestAChildPublishesItsExitOnce(t *testing.T) {
	spawned := &child{gone: make(chan struct{})}
	select {
	case <-spawned.gone:
		t.Fatal("gone is closed before the exit was published")
	default:
	}
	spawned.publish()
	select {
	case <-spawned.gone:
	default:
		t.Fatal("gone is not closed after the exit was published")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a second publication of the exit panicked: %v", r)
		}
	}()
	spawned.publish()
}
