package host_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func TestTerminalModesAdmitted7MouseOnlyByPolicyTrackedUndoneOnCleanupAndRestoredAfterLauncherUI(t *testing.T) {
	filtered, _ := host.FilterTerminalOutput("\x1b[?7l\x1b[?1000h", host.BaseModes)
	if filtered != "\x1b[?7l" {
		t.Fatalf("base filter = %q", filtered)
	}
	off := host.NewTerminalModes(host.MouseOff)
	got, _ := off.Filter("a\x1b[?7l\x1b[?1000h\x1b[?1003h\x1b[?2004h\x1b[?25lb")
	if got != "a\x1b[?7l\x1b[?2004h\x1b[?25lb" {
		t.Fatalf("filter = %q", got)
	}
	if got := off.Restore(); got != "\x1b[?7l\x1b[?2004h\x1b[?25l" {
		t.Fatalf("restore = %q", got)
	}
	cleanup := off.Cleanup()
	if !strings.HasPrefix(cleanup, "\x1b[?7h\x1b[?2004l\x1b[?25h") || !strings.HasSuffix(cleanup, host.TerminalReset) {
		t.Fatalf("cleanup = %q", cleanup)
	}
	if got := off.Restore(); got != "" {
		t.Fatalf("cleanup forgets the worker's state: %q", got)
	}
	on := host.NewTerminalModes(host.MouseOn)
	if got, _ := on.Filter("\x1b[?1000h\x1b[?1006h\x1b[?1003h"); got != "\x1b[?1000h\x1b[?1006h" {
		t.Fatalf("on filter = %q", got)
	}
	if !strings.HasPrefix(on.Cleanup(), "\x1b[?1000l\x1b[?1006l") {
		t.Fatal("on cleanup")
	}
	if got, _ := host.NewTerminalModes(host.MouseAll).Filter("\x1b[?1003h"); got != "\x1b[?1003h" {
		t.Fatalf("all filter = %q", got)
	}
	for _, mode := range []string{"1000", "1002", "1003", "1006", "1004", "2004", "2026", "1049"} {
		if !strings.Contains(host.TerminalReset, "\x1b[?"+mode+"l") {
			t.Fatal(mode)
		}
	}
	if !strings.Contains(host.TerminalReset, "\x1b[?7h") || !strings.Contains(host.TerminalReset, "\x1b[?25h") || !strings.Contains(host.TerminalReset, "\x1b[r") {
		t.Fatal("reset")
	}
}

type recorder struct {
	mu      sync.Mutex
	written []string
	columns int
	rows    int
}

func (r *recorder) Write(data string) {
	r.mu.Lock()
	r.written = append(r.written, data)
	r.mu.Unlock()
}
func (r *recorder) Columns() int { return r.columns }
func (r *recorder) Rows() int    { return r.rows }
func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.written, "")
}

func confirmAsync(ui *host.TrustedUi, request host.ConsentRequest) chan bool {
	result := make(chan bool, 1)
	go func() { result <- ui.Confirm(request) }()
	return result
}

func waitActive(t *testing.T, ui *host.TrustedUi) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ui.Active() {
		if time.Now().After(deadline) {
			t.Fatal("the UI never showed")
		}
		time.Sleep(time.Millisecond)
	}
}

func answer(t *testing.T, result chan bool) bool {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
		return false
	}
}

func TestTrustedUIOnlyEnterOrYConfirmsDataIsShownWithVisibleControlCharacters(t *testing.T) {
	out := &recorder{columns: 60, rows: 12}
	ui := host.NewTrustedUi(out, "https://server.test")
	closed := 0
	ui.OnClose = func() { closed++ }
	first := confirmAsync(ui, host.ConsentRequest{Action: "Copy", Preview: "line\x1b]52;c;evil\x07\nsecond", HasPreview: true})
	waitActive(t, ui)
	screen := out.text()
	if !strings.Contains(screen, "Server: https://server.test") || !strings.Contains(screen, "line␛]52;c;evil␇") || strings.Contains(screen, "\x1b]52") {
		t.Fatalf("screen = %q", screen)
	}
	for _, key := range []string{"a", " ", "\x1b[A", "\x1b[6~", "\x04"} {
		ui.HandleInput(key)
	}
	if !ui.Active() {
		t.Fatal("unrelated keys neither confirm nor cancel")
	}
	ui.HandleInput("\x1b")
	if answer(t, first) {
		t.Fatal("escape confirmed")
	}
	second := confirmAsync(ui, host.ConsentRequest{Action: "Copy"})
	waitActive(t, ui)
	if ui.Confirm(host.ConsentRequest{Action: "Other"}) {
		t.Fatal("one interaction at a time")
	}
	ui.HandleInput("n")
	if answer(t, second) {
		t.Fatal("n confirmed")
	}
	third := confirmAsync(ui, host.ConsentRequest{Action: "Copy"})
	waitActive(t, ui)
	ui.HandleInput("y")
	if !answer(t, third) {
		t.Fatal("y did not confirm")
	}
	fourth := confirmAsync(ui, host.ConsentRequest{Action: "Copy"})
	waitActive(t, ui)
	ui.HandleInput("\r")
	if !answer(t, fourth) {
		t.Fatal("enter did not confirm")
	}
	if closed != 4 {
		t.Fatalf("closed %d", closed)
	}
	if got := host.VisibleText("\x00\u009b\x7f"); got != "␀\ufffd␡" {
		t.Fatalf("visible = %q", got)
	}
}

func TestGrantsBindToTheWorkerAndTargetSwapsNavigationAndExpiryCancelThem(t *testing.T) {
	grants := host.NewGrants("https://server.test")
	worker, other := new(int), new(int)
	target := &host.GrantTarget{Session: "s", Conversation: 1}
	grant := grants.Create("copy", host.GrantC, worker, target)
	if grants.Valid(grant.ID, worker, "copy") == nil {
		t.Fatal("valid")
	}
	if grants.Valid(grant.ID, other, "") != nil {
		t.Fatal("another worker cannot use it")
	}
	if grants.Valid(grant.ID, worker, "openUrl") != nil {
		t.Fatal("nor another action")
	}
	grants.Navigate(worker, target)
	if grants.Valid(grant.ID, worker, "") == nil {
		t.Fatal("same target keeps it")
	}
	grants.Navigate(worker, &host.GrantTarget{Session: "s", Conversation: 2})
	if grants.Valid(grant.ID, worker, "") != nil || grant.Context.Err() == nil {
		t.Fatal("navigation cancels")
	}
	swapped := grants.Create("copy", host.GrantK, worker, nil)
	grants.CancelWorker(worker)
	if swapped.Context.Err() == nil {
		t.Fatal("swap cancels")
	}
	expired := grants.CreateTTL("copy", host.GrantC, worker, nil, -time.Second)
	if grants.Valid(expired.ID, worker, "") != nil {
		t.Fatal("expired")
	}
	if grants.Size() != 0 {
		t.Fatalf("size %d", grants.Size())
	}
	if got := host.ParseTarget("abc-1", 2); got == nil || *got != (host.GrantTarget{Session: "abc-1", Conversation: 2}) {
		t.Fatalf("target = %v", got)
	}
	if host.ParseTarget("../x", 1) != nil || host.ParseTarget("s", 0) != nil {
		t.Fatal("invalid targets")
	}
}

func TestPrivilegedKeysOnlyRegisteredActionsAreInterceptedWithTheVisitorsLocalBindings(t *testing.T) {
	const ctrlX = "\x18"
	platform := prefs.KeyPlatform{Platform: "linux"}
	copyOnly := []string{"app.message.copy"}
	keys := host.NewPrivilegedKeys(pitui.KeybindingsConfig{}, platform, copyOnly)
	if id, release, ok := keys.Match(ctrlX); !ok || id != "app.message.copy" || release {
		t.Fatalf("match = %q %v %v", id, release, ok)
	}
	if _, _, ok := keys.Match("\x16"); ok {
		t.Fatal("Ctrl+V has no registered action here")
	}
	if _, _, ok := keys.Match("x"); ok {
		t.Fatal("x")
	}
	rebound := host.NewPrivilegedKeys(pitui.KeybindingsConfig{"app.message.copy": {"f9"}}, platform, copyOnly)
	if _, _, ok := rebound.Match(ctrlX); ok {
		t.Fatal("rebound still on Ctrl+X")
	}
	if id, _, ok := rebound.Match("\x1b[20~"); !ok || id != "app.message.copy" {
		t.Fatal("F9")
	}
	// Ctrl+Y is pi's yank: a privileged action bound there loses it.
	if _, _, ok := host.NewPrivilegedKeys(pitui.KeybindingsConfig{"app.message.copy": {"ctrl+y"}}, platform, copyOnly).Match("\x19"); ok {
		t.Fatal("ctrl+y")
	}
	// A local binding that takes the key for a non-privileged action wins over the launcher action (rule 2).
	if _, _, ok := host.NewPrivilegedKeys(pitui.KeybindingsConfig{"app.model.select": {"ctrl+x"}}, platform, copyOnly).Match(ctrlX); ok {
		t.Fatal("lost key matched")
	}
}
