package codingagent

import "testing"

// interactive-mode.ts:4246-4249 getUserInput: queued prompts are taken first-in first-out (pendingUserInputs.shift()) and the rest stay queued; with the queue
// empty the next prompt arrives through the one-shot input callback (:3391-3394), which a later submit then queues instead.
func TestGetUserInputTakesQueuedPromptsInOrderThenWaitsForTheCallback(t *testing.T) {
	m := &InteractiveMode{pendingUserInputs: []string{"first", "second", "third"}}
	for i, want := range []string{"first", "second", "third"} {
		select {
		case got := <-m.getUserInput():
			if got != want {
				t.Fatalf("queued prompt %d = %q, want %q", i, got, want)
			}
		default:
			t.Fatalf("queued prompt %d was not ready", i)
		}
		if remaining := len(m.pendingUserInputs); remaining != 2-i {
			t.Fatalf("after prompt %d %d remain queued, want %d", i, remaining, 2-i)
		}
	}
	if m.onInputCallback != nil {
		t.Fatal("a queued prompt installed the input callback")
	}

	waiting := m.getUserInput()
	select {
	case got := <-waiting:
		t.Fatalf("an empty queue returned %q", got)
	default:
	}
	if m.onInputCallback == nil {
		t.Fatal("an empty queue installed no input callback")
	}
	m.onInputCallback("typed")
	if got := <-waiting; got != "typed" {
		t.Fatalf("callback prompt = %q", got)
	}
	if m.onInputCallback != nil {
		t.Fatal("the callback stayed installed after one prompt")
	}
}
