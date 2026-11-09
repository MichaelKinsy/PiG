package host_test

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const ctrlX = "\x18"

type inputLog struct {
	mu   sync.Mutex
	data []string
}

func (l *inputLog) add(data string) {
	l.mu.Lock()
	l.data = append(l.data, data)
	l.mu.Unlock()
}

func (l *inputLog) has(data string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.data, data)
}

func (l *inputLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.data)
}

func TestIsolationCActionsNeedLocalConfirmationKActionsFireOnlyOnTheirKeyWorkerIOIsSuspended(t *testing.T) {
	var mu sync.Mutex
	var ran, keyed []string
	inputs := &inputLog{}
	l := startLauncher(t, launcherOptions{
		actions: func(map[string]host.LauncherAction) map[string]host.LauncherAction {
			return map[string]host.LauncherAction{
				"test.confirm": {
					Kind: host.GrantC,
					Consent: func(payload any, _ host.ConsentContext) *host.ConsentRequest {
						if text, ok := record(payload)["text"].(string); ok {
							return &host.ConsentRequest{Action: "Use the text", Preview: text, HasPreview: true}
						}
						return nil
					},
					Run: func(payload any, ctx host.ActionContext) (any, error) {
						encoded, _ := json.Marshal(payload)
						session := ""
						if ctx.Grant.Target != nil {
							session = ctx.Grant.Target.Session
						}
						mu.Lock()
						ran = append(ran, string(encoded)+"@"+session)
						mu.Unlock()
						return "done", nil
					},
				},
				"test.key": {
					Kind: host.GrantK, Key: "app.message.copy",
					RunKey: func(ctx host.ActionContext) error {
						answer, err := ctx.Ask("copyText", nil)
						encoded, _ := json.Marshal(answer)
						mu.Lock()
						keyed = append(keyed, string(encoded))
						mu.Unlock()
						return err
					},
				},
			}
		},
		options: func(o *host.ClientOptions) { o.InputTap = inputs.add },
	})
	rt := l.client.Runtime()
	l.client.Do(func() {
		rt.LauncherAnswers["copyText"] = func(any) any { return map[string]any{"text": "from-worker"} }
	})
	if contains(l.terminal.written(), "\x1b[?1000h") {
		t.Fatal("mouse modes need the visitor's policy")
	}
	count := func(list *[]string) int { mu.Lock(); defer mu.Unlock(); return len(*list) }

	// K: the privileged key fires its action and never reaches the worker; other keys do.
	l.terminal.SendInput(ctrlX)
	poll(t, func() bool { return count(&keyed) == 1 })
	if keyed[0] != `{"text":"from-worker"}` {
		t.Fatalf("keyed = %v", keyed)
	}
	l.terminal.SendInput("z")
	poll(t, func() bool { return inputs.has("z") })
	if inputs.has(ctrlX) {
		t.Fatal("the privileged key reached the worker")
	}
	// Rule 3: a suspended context forwards it (e.g. search-next) and nothing fires.
	var overlay pitui.Component = pitui.NewText("search")
	l.client.Do(func() { l.client.TUI().SetFocus(overlay); rt.Changed() })
	l.terminal.SendInput("s")
	poll(t, func() bool { return inputs.has("s") })
	l.terminal.SendInput(ctrlX)
	poll(t, func() bool { return inputs.has(ctrlX) })
	if count(&keyed) != 1 {
		t.Fatal("a suspended context fired the action")
	}
	l.client.Do(func() { l.client.TUI().SetFocus(rt); rt.Changed() })

	// C: denied with Escape, with n; never run without a confirmation; worker output and input suspended.
	l.client.Do(func() { rt.Navigate("chat", "s1", true, nil) })
	for _, cancel := range []string{"\x1b", "n"} {
		answer := l.grant("test.confirm", map[string]any{"text": "hello"})
		poll(t, func() bool { return contains(l.terminal.written(), "Action: Use the text") })
		l.terminal.clear()
		before := inputs.len()
		l.client.Do(func() { rt.Notify("WORKER-OUTPUT", tuiruntime.NoticeInfo) })
		l.terminal.SendInput("a")
		time.Sleep(50 * time.Millisecond)
		if inputs.len() != before {
			t.Fatal("input is consumed by the launcher UI")
		}
		during := l.terminal.written()
		l.terminal.SendInput(cancel)
		if got := awaitAnswer(t, answer); got.Error != "denied" {
			t.Fatalf("cancel %q: %+v", cancel, got)
		}
		// A full redraw follows launcher UI.
		poll(t, func() bool { return contains(l.terminal.screen(), "WORKER-OUTPUT") })
		if count(&ran) != 0 {
			t.Fatal("ran without a confirmation")
		}
		if strings.Contains(during, "WORKER-OUTPUT") {
			t.Fatal("worker output reached the terminal while launcher UI showed")
		}
		l.client.Do(func() { rt.ClearNotices() })
	}
	// The earlier consents' text is in what was written: clear it, or the wait below returns before this consent shows and the key is lost.
	l.terminal.clear()
	answer := l.grant("test.confirm", map[string]any{"text": "hello"})
	poll(t, func() bool { return contains(l.terminal.written(), "hello") })
	l.terminal.SendInput("\r")
	if got := awaitAnswer(t, answer); !got.OK || got.Value != "done" {
		t.Fatalf("confirmed: %+v", got)
	}
	if !slices.Equal(ran, []string{`{"text":"hello"}@s1`}) {
		t.Fatalf("ran = %v", ran)
	}

	// Navigation while the consent shows cancels the grant: confirming then runs nothing.
	l.terminal.clear()
	answer = l.grant("test.confirm", map[string]any{"text": "hello"})
	poll(t, func() bool { return contains(l.terminal.written(), "Session: s1 #1") })
	l.client.Do(func() { rt.Navigate("chat", "s2", true, nil); rt.Changed() })
	l.terminal.SendInput("y")
	if got := awaitAnswer(t, answer); got.Error != "cancelled" || count(&ran) != 1 {
		t.Fatalf("navigated: %+v ran %v", got, ran)
	}
	// Unknown actions are denied without any UI.
	if got := awaitAnswer(t, l.grant("unknown.action", map[string]any{})); got.Error != "denied" {
		t.Fatalf("unknown: %+v", got)
	}

	// N: titles are sanitized and rate limited.
	time.Sleep(1100 * time.Millisecond)
	base := len(l.terminal.titleList())
	l.client.Do(func() {
		for i := range 10 {
			l.client.Terminal().SetTitle("T" + string(rune('0'+i)) + "\x1b]52;c;x\x07")
		}
	})
	poll(t, func() bool { return len(l.terminal.titleList()) >= base+4 })
	time.Sleep(100 * time.Millisecond)
	titles := l.terminal.titleList()[base:]
	if len(titles) != 4 || strings.Contains(strings.Join(titles, ""), "\x1b") {
		t.Fatalf("titles = %q", titles)
	}
	pollFor(t, 3*time.Second, func() bool { return len(l.terminal.titleList()) == base+5 })
	if got := l.terminal.titleList()[base+4]; got != "T9 ]52;c;x " {
		t.Fatalf("latest throttled title = %q", got)
	}

	// Crash: the launcher closes and resets everything.
	l.terminal.clear()
	l.client.Do(func() { rt.OnExit() })
	poll(t, func() bool { return contains(l.terminal.written(), host.TerminalReset) })
}
