package tui

import (
	"strings"
	"testing"
)

// A login goroutine writes the dialog's input and redaction state under the dialog's lock (an OAuth flow's callbacks and the device-code
// clipboard goroutine call the mutators off the loop), so every read of that state by a frame holds the lock too. The parent's settle scan
// reaches a child that embeds a Container through the promoted Container.IsDirty, which renders the child's own children; before the
// dialog overrode it, that scan read d.input and d.secrets without d.mu (go test -race: Read at redactLocked, Previous write at showInput).

// lockProbe is a child of the dialog's content container that records whether its caller holds the dialog's lock while it renders.
type lockProbe struct {
	dialog            *LoginDialogComponent
	renders, unlocked int
}

func (p *lockProbe) Render(int) []string {
	p.renders++
	if p.dialog.mu.TryLock() {
		p.dialog.mu.Unlock()
		p.unlocked++
	}
	return []string{"probe"}
}

func (*lockProbe) Invalidate() {}

func TestLoginDialogChildrenRenderUnderTheDialogLockWhenAParentScansForChanges(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Lock", nil, "")
	probe := &lockProbe{dialog: dialog}
	dialog.content.Add(probe)
	parent := NewContainer(dialog)
	for range 4 {
		parent.Render(contractWidth)
	}
	if probe.renders < 2 {
		t.Fatalf("the probe rendered %d times in four frames: the parent's settle scan never reached the dialog's children", probe.renders)
	}
	if probe.unlocked != 0 {
		t.Fatalf("%d of %d renders of a dialog child ran without the dialog's lock", probe.unlocked, probe.renders)
	}
}

// An OAuth flow's provider goroutine shows prompts, auth URLs and progress while the loop types into the prompt and the parent renders the
// frame. Every render reads the redaction state (secrets, the active input) that those mutators write, and redactLocked runs inside the
// render under the dialog's lock, so no step may take the lock again: a deadlock ends this test by the test binary's timeout. The loop's
// frames run until the worker has finished, so the two always overlap.
func TestLoginDialogMutatedByALoginGoroutineRendersRaceFreeThroughAParent(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Race", nil, "")
	dialog.SetFocused(true)
	parent := NewContainer(dialog)
	parent.Render(contractWidth)
	framing, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		<-framing
		for i := range 400 {
			switch i % 4 {
			case 0:
				dialog.ShowSecretInput("Key:", "sk-")
			case 1:
				dialog.ShowProgress("working")
			case 2:
				dialog.ShowAuth("https://example.test/auth", "open it")
			default:
				_ = dialog.Redact("sk-typed")
			}
		}
	}()
	close(framing)
	for running := true; running; {
		select {
		case <-finished:
			running = false
		default:
			dialog.HandleInput("k")
			parent.Render(contractWidth)
		}
	}
	if got := strings.Join(plain(parent.Render(contractWidth)), "\n"); !strings.Contains(got, "Login to Race") {
		t.Fatalf("the dialog lost its title:\n%s", got)
	}
}

// The clipboard writer of a confirmed device code runs on its own goroutine and sets the hint row under the dialog's lock when it returns;
// the loop's frames render that row meanwhile.
func TestLoginDialogDeviceCodeCopyOutcomeRendersRaceFreeThroughAParent(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Device", nil, "")
	dialog.SetFocused(true)
	started, release, repainted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	dialog.SetCopyToClipboard(func(string) error {
		close(started)
		<-release
		return nil
	}, func() { close(repainted) })
	dialog.ShowDeviceCode(OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: "https://example.test/device"})
	parent := NewContainer(dialog)
	parent.Render(contractWidth)
	dialog.HandleInput("\r")
	<-started
	if got := strings.Join(plain(parent.Render(contractWidth)), "\n"); !strings.Contains(got, "Copying code...") {
		t.Fatalf("the hint did not show the copy in progress:\n%s", got)
	}
	close(release)
	for repainted != nil {
		select {
		case <-repainted:
			repainted = nil
		default:
			parent.Render(contractWidth)
		}
	}
	if got := strings.Join(plain(parent.Render(contractWidth)), "\n"); !strings.Contains(got, "Code copied to clipboard") {
		t.Fatalf("the hint did not show the copy outcome:\n%s", got)
	}
}

// AuthURL is the dialog's sibling: its copy goroutine sets the outcome under its own mutex while the loop's frames render the hint row. The hint
// child reads the outcome through texts(), which takes that mutex on every render, so the promoted Container.IsDirty needs no override.
func TestAuthURLCopyOutcomeRendersRaceFreeThroughAParent(t *testing.T) {
	release := make(chan struct{})
	auth := NewAuthURL("https://example.test/auth", func(string) error {
		<-release
		return nil
	}, func() {})
	parent := NewContainer(auth)
	parent.Render(contractWidth)
	done := auth.Copy()
	close(release)
	for done != nil {
		select {
		case <-done:
			done = nil
		default:
			parent.Render(contractWidth)
		}
	}
	if got := strings.Join(plain(parent.Render(contractWidth)), "\n"); !strings.Contains(got, "Copied URL to clipboard") {
		t.Fatalf("the hint did not show the copy outcome:\n%s", got)
	}
}
