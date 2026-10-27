package tui

// Ports packages/coding-agent/test/login-dialog-device-code.test.ts (Pi #9282): device-code convenience actions require explicit confirmation.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var deviceCodeInfo = OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: "https://example.com/device"}

type deviceCodeHarness struct {
	dialog    *LoginDialogComponent
	clipboard *clipboardRecorder
	mu        sync.Mutex
	opened    []string
	copyErr   error
	completed []string
}

func newDeviceCodeHarness(t *testing.T) *deviceCodeHarness {
	t.Helper()
	authCopyKeybindings(t)
	h := &deviceCodeHarness{clipboard: &clipboardRecorder{}}
	h.dialog = NewLoginDialogComponent(nil, "custom-provider", func(success bool, message string) {
		h.completed = append(h.completed, message)
	}, "")
	h.dialog.SetCopyToClipboard(func(text string) error {
		if h.copyErr != nil {
			return h.copyErr
		}
		return h.clipboard.copy(text)
	}, nil)
	h.dialog.SetOpenBrowser(func(url string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.opened = append(h.opened, url)
	})
	h.dialog.SetFocused(true)
	return h
}

func (h *deviceCodeHarness) openedURLs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.opened...)
}

// settle waits until the dialog renders want: the clipboard write runs off the input path, like Pi's unawaited copyToClipboard.
func (h *deviceCodeHarness) settle(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(renderedPlain(h.dialog), want) {
		if time.Now().After(deadline) {
			t.Fatalf("dialog never showed %q:\n%s", want, renderedPlain(h.dialog))
		}
		time.Sleep(time.Millisecond)
	}
}

func sameStrings(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDeviceCodeLoginOpensTheVerificationPageAndCopiesTheCodeOnceAfterConfirmation(t *testing.T) {
	h := newDeviceCodeHarness(t)
	answer := h.dialog.ShowPrompt("GitHub Enterprise URL/domain (blank for github.com)", "")
	h.dialog.HandleInput("\r")
	if got := <-answer; got != "" {
		t.Fatalf("prompt answer = %q, want empty", got)
	}
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.ShowWaiting("Waiting for authentication...")
	if got := renderedPlain(h.dialog); !strings.Contains(got, "to open browser and copy code") {
		t.Fatalf("dialog lacks the offer:\n%s", got)
	}
	if len(h.openedURLs()) != 0 || len(h.clipboard.texts()) != 0 {
		t.Fatal("an action ran before confirmation")
	}

	h.dialog.HandleInput("\r")
	h.dialog.HandleInput("\r")
	h.settle(t, "Code copied to clipboard")
	if got := h.openedURLs(); !sameStrings(got, deviceCodeInfo.VerificationURI) {
		t.Fatalf("opened = %q, want one %q", got, deviceCodeInfo.VerificationURI)
	}
	if got := h.clipboard.texts(); !sameStrings(got, deviceCodeInfo.UserCode) {
		t.Fatalf("copied = %q, want one %q", got, deviceCodeInfo.UserCode)
	}
	got := renderedPlain(h.dialog)
	for _, want := range []string{"Enter code: " + deviceCodeInfo.UserCode, "Waiting for authentication..."} {
		if !strings.Contains(got, want) {
			t.Errorf("dialog lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "to open browser and copy code") {
		t.Errorf("the offer stayed after the confirmation:\n%s", got)
	}
}

func TestDeviceCodeLoginHonorsProviderOverrides(t *testing.T) {
	no, yes := false, true
	for _, tc := range []struct {
		name          string
		browser, cp   *bool
		opens, copies int
	}{
		{"browser=false clipboard=false", &no, &no, 0, 0},
		{"browser=true clipboard=false", &yes, &no, 1, 0},
		{"browser=false clipboard=true", &no, &yes, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDeviceCodeHarness(t)
			info := deviceCodeInfo
			info.OpenBrowserOnConfirm, info.CopyCodeOnConfirm = tc.browser, tc.cp
			h.dialog.ShowDeviceCode(info)
			h.dialog.HandleInput("\r")
			if tc.copies > 0 {
				h.settle(t, "Code copied to clipboard")
			}
			if got := len(h.openedURLs()); got != tc.opens {
				t.Errorf("browser opened %d times, want %d", got, tc.opens)
			}
			if got := len(h.clipboard.texts()); got != tc.copies {
				t.Errorf("clipboard written %d times, want %d", got, tc.copies)
			}
			if h.dialog.Cancelled() {
				t.Error("the dialog cancelled the login")
			}
		})
	}
}

func TestDeviceCodeLoginClearsThePreviousAuthURLCopyActionWhenShowingADeviceCode(t *testing.T) {
	h := newDeviceCodeHarness(t)
	h.dialog.ShowAuth("https://example.com/authorize", "")
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.HandleInput("\x18")
	if len(h.clipboard.texts()) != 0 || len(h.openedURLs()) != 0 {
		t.Fatal("the auth URL copy key acted on a device-code dialog")
	}
	h.dialog.HandleInput("\r")
	h.settle(t, "Code copied to clipboard")
	if got := h.clipboard.texts(); !sameStrings(got, deviceCodeInfo.UserCode) {
		t.Fatalf("copied = %q, want the device code once", got)
	}
}

func TestDeviceCodeLoginClearsDeviceActionsWhenSwitchingToBrowserAuthentication(t *testing.T) {
	h := newDeviceCodeHarness(t)
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.ShowAuth("https://example.com/authorize", "")
	h.dialog.HandleInput("\r")
	if len(h.clipboard.texts()) != 0 || len(h.openedURLs()) != 0 {
		t.Fatal("the confirm key acted on a browser-auth dialog")
	}
	h.dialog.HandleInput("\x18")
	h.settle(t, "Copied URL to clipboard")
	if got := h.clipboard.texts(); !sameStrings(got, "https://example.com/authorize") {
		t.Fatalf("copied = %q, want the auth URL once", got)
	}
}

func TestDeviceCodeLoginClearsBothActionTypesWhenShowingDetails(t *testing.T) {
	h := newDeviceCodeHarness(t)
	h.dialog.ShowAuth("https://example.com/authorize", "")
	h.dialog.ShowDetails([]string{"Choose a login method"})
	h.dialog.HandleInput("\x18")
	if len(h.clipboard.texts()) != 0 {
		t.Fatal("the auth URL copy key acted after details")
	}
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.ShowDetails([]string{"Choose a login method"})
	h.dialog.HandleInput("\r")
	if len(h.clipboard.texts()) != 0 || len(h.openedURLs()) != 0 {
		t.Fatal("the confirm key acted after details")
	}
}

func TestDeviceCodeLoginCancelsWithoutOpeningABrowserOrChangingTheClipboard(t *testing.T) {
	h := newDeviceCodeHarness(t)
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.HandleInput("\x1b")
	h.dialog.HandleInput("\r")
	if !h.dialog.Cancelled() || !sameStrings(h.completed, "Login cancelled") {
		t.Fatalf("cancelled=%v completed=%q, want one Login cancelled", h.dialog.Cancelled(), h.completed)
	}
	if len(h.clipboard.texts()) != 0 || len(h.openedURLs()) != 0 {
		t.Fatal("an action ran after cancellation")
	}
}

func TestDeviceCodeLoginKeepsManualLoginAvailableWhenBrowserAndClipboardAccessFail(t *testing.T) {
	h := newDeviceCodeHarness(t)
	h.copyErr = errors.New("Clipboard unavailable")
	h.dialog.ShowDeviceCode(deviceCodeInfo)
	h.dialog.ShowWaiting("Waiting for authentication...")
	h.dialog.HandleInput("\r")
	h.settle(t, "Copy code manually")
	got := renderedPlain(h.dialog)
	for _, want := range []string{deviceCodeInfo.VerificationURI, "Enter code: " + deviceCodeInfo.UserCode, "Waiting for authentication..."} {
		if !strings.Contains(got, want) {
			t.Errorf("dialog lacks %q:\n%s", want, got)
		}
	}
	if h.dialog.Cancelled() || len(h.completed) != 0 {
		t.Fatalf("login ended: cancelled=%v completed=%q", h.dialog.Cancelled(), h.completed)
	}
}

func TestDeviceCodeLoginOpensOnlyWebURLs(t *testing.T) {
	h := newDeviceCodeHarness(t)
	info := deviceCodeInfo
	info.VerificationURI = "file:///etc/passwd"
	h.dialog.ShowDeviceCode(info)
	h.dialog.HandleInput("\r")
	h.settle(t, "Code copied to clipboard")
	if got := h.openedURLs(); len(got) != 0 {
		t.Fatalf("opened %q, want nothing", got)
	}
}

// Ports login-dialog-device-code.test.ts "login URLs" / "opens only http(s) sign-in URLs": ShowAuth names the URL its caller opens.
func TestLoginURLsOpenOnlyHTTPSignInURLs(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "custom-provider", nil, "")
	if target := dialog.ShowAuth("file:///Users/victim/Downloads/payload.app", ""); target != "" {
		t.Fatalf("a file: URL is opened as %q", target)
	}
	if target := dialog.ShowAuth("https://example.com/authorize", ""); target != "https://example.com/authorize" {
		t.Fatalf("target = %q", target)
	}
	if target := dialog.ShowAuth("HTTPS://Example.com", ""); target != "https://example.com/" {
		t.Fatalf("a web URL is opened as its normalized href, got %q", target)
	}
}

// Ports login-dialog-device-code.test.ts "keeps control characters in URLs from ending terminal hyperlinks".
func TestLoginURLsKeepControlCharactersFromEndingTerminalHyperlinks(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "custom-provider", nil, "")
	dialog.ShowDeviceCode(OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: "https://example.com/device\x07\x1b]52;c;Zm9v\x07"})
	deviceView := renderedPlain(dialog)
	deviceRaw := strings.Join(dialog.Render(120), "\n")
	dialog.ShowInfo("Docs", []AuthInfoLink{{Label: "Help", URL: "https://example.com/help\x07\x1b]52;c;Zm9v\x07"}}, false)
	infoRaw := strings.Join(dialog.Render(120), "\n")

	if strings.Contains(deviceRaw, "\x1b]52") || strings.Contains(infoRaw, "\x1b]52") {
		t.Fatalf("an injected OSC 52 reached the terminal:\n%q\n%q", deviceRaw, infoRaw)
	}
	if !strings.Contains(deviceView, "https://example.com/device%07%1B]52;c;Zm9v") {
		t.Fatalf("device view = %q", deviceView)
	}
	if view := renderedPlain(dialog); !strings.Contains(view, "Help: https://example.com/help%07%1B]52;c;Zm9v") {
		t.Fatalf("info view = %q", view)
	}
}

// Ports login-dialog-device-code.test.ts "replaces control characters in URLs that are not web URLs".
func TestLoginURLsReplaceControlCharactersInURLsThatAreNotWebURLs(t *testing.T) {
	injected := "x\x07\x1b]52;c;Zm9v\x07"
	dialog := NewLoginDialogComponent(nil, "custom-provider", nil, "")
	if target := dialog.ShowAuth("file:///tmp/"+injected, ""); target != "" {
		t.Fatalf("a file: URL is opened as %q", target)
	}
	if raw := strings.Join(dialog.Render(120), "\n"); strings.Contains(raw, "\x1b]52") {
		t.Fatalf("auth view carries an injected OSC 52: %q", raw)
	}

	dialog.ShowDeviceCode(OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: injected})
	if raw := strings.Join(dialog.Render(120), "\n"); strings.Contains(raw, "\x1b]52") {
		t.Fatalf("device view carries an injected OSC 52: %q", raw)
	}
	if view := renderedPlain(dialog); !strings.Contains(view, "x ]52;c;Zm9v") {
		t.Fatalf("device view = %q", view)
	}

	dialog.ShowInfo("Docs", []AuthInfoLink{{Label: "Help", URL: injected}}, false)
	if raw := strings.Join(dialog.Render(120), "\n"); strings.Contains(raw, "\x1b]52") {
		t.Fatalf("info view carries an injected OSC 52: %q", raw)
	}
	if view := renderedPlain(dialog); !strings.Contains(view, "Help: x ]52;c;Zm9v") {
		t.Fatalf("info view = %q", view)
	}
}
