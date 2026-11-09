package host_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestKittyKeyboardTheLaunchersNegotiatedProtocolReachesTheRuntimeWhichDecodesKeysAccordingly(t *testing.T) {
	typeInto := func(kitty bool) (draft, screen string) {
		l := startReal(t, launcherOptions{kitty: &kitty})
		l.openNewestSession()
		pollFor(t, testbudget.Wait(t), func() bool { return l.client.Snapshot().Screen == "chat" })
		l.terminal.SendInput("a")
		l.terminal.SendInput("\x1b\r")
		l.terminal.SendInput("b")
		// Each protocol ends in an observable state: Kitty's newline leaves "a\nb" in the draft, legacy's alt+enter sends "a" and the fake provider echoes it. The wait only bounds a hang; a state that never comes is reported by the assertions below.
		settled := func() bool {
			if kitty {
				return l.draft() == "a\nb"
			}
			return contains(l.terminal.screen(), "Echo: a")
		}
		for deadline := time.Now().Add(testbudget.Wait(t)); !settled() && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		return l.draft(), l.terminal.screen()
	}
	// Kitty: ESC CR is Kitty's shift+enter mapping, a newline. Legacy: it is alt+enter, the follow-up that sends "a".
	draft, screen := typeInto(true)
	if draft != "a\nb" || contains(screen, "Echo: a") {
		t.Fatalf("kitty: draft %q screen %q", draft, screen)
	}
	if _, screen := typeInto(false); !contains(screen, "Echo: a") {
		t.Fatalf("legacy: %q", screen)
	}
}

func TestKeyContextWhileTranscriptSearchHasFocusTheRuntimeSuspendsInterceptionSoCtrlGIsSearchNext(t *testing.T) {
	var edited atomic.Int32
	l := startReal(t, launcherOptions{actions: func(base map[string]host.LauncherAction) map[string]host.LauncherAction {
		// Pi's test passes only this action; two K actions on one key would be picked by map order, so the default editor action goes.
		delete(base, "editor.external")
		base["test.editor"] = host.LauncherAction{Kind: host.GrantK, Key: "app.editor.external", RunKey: func(host.ActionContext) error {
			edited.Add(1)
			return nil
		}}
		return base
	}})
	l.openNewestSession()
	l.terminal.SendInput(keyCtrlG)
	poll(t, func() bool { return edited.Load() == 1 })
	// Ctrl+Shift+F (kitty-encoded) opens the transcript search; the runtime reports the suspension.
	l.terminal.SendInput("\x1b[102;6u")
	time.Sleep(300 * time.Millisecond)
	l.terminal.SendInput(keyCtrlG)
	time.Sleep(300 * time.Millisecond)
	if edited.Load() != 1 {
		t.Fatal("Ctrl+G went to the search")
	}
	l.terminal.SendInput(keyEsc)
	time.Sleep(300 * time.Millisecond)
	l.terminal.SendInput(keyCtrlG)
	poll(t, func() bool { return edited.Load() == 2 })
}
