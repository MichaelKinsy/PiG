package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports .upstream/v1.0.1/packages/coding-agent/test/auth-url-copy.test.ts ("sign-in URL copy key", login dialog cases).
// The MCP sign-in screen case is in coding/mcpext/manager_test.go.

var authCopyURL = "https://auth.example.invalid/authorize?" + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

var strings300 = strings.Repeat("x", 300)

// authCopyKeybindings installs the copy key the coding agent's merged table binds.
func authCopyKeybindings(t *testing.T) {
	t.Helper()
	prev := GetTUIKeybindings()
	t.Cleanup(func() { SetKeybindings(prev) })
	defs := TUIKeybindingDefinitionsFor(KeybindingPlatformFor("linux", func(string) string { return "" }))
	defs["app.message.copy"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+x"}}
	SetKeybindings(NewKeybindingsManager(defs, nil))
}

type clipboardRecorder struct {
	mu     sync.Mutex
	copied []string
}

func (c *clipboardRecorder) copy(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.copied = append(c.copied, text)
	return nil
}

func (c *clipboardRecorder) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.copied...)
}

func renderedPlain(d *LoginDialogComponent) string {
	return widthx.StripAnsi(strings.Join(d.Render(80), "\n"))
}

func TestAuthURLCopyLoginDialogCopiesTheAuthURLInsteadOfTypingIntoTheCodeInput(t *testing.T) {
	authCopyKeybindings(t)
	clipboard := &clipboardRecorder{}
	dialog := NewLoginDialogComponent(nil, "test", nil, "")
	dialog.SetCopyToClipboard(clipboard.copy, nil)
	dialog.ShowAuth(authCopyURL, "")
	dialog.ShowManualInput("Paste the code:")
	if got := renderedPlain(dialog); !strings.Contains(got, "ctrl+x to copy") {
		t.Fatalf("dialog lacks the copy hint:\n%s", got)
	}
	dialog.HandleInput("\x18")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(renderedPlain(dialog), "Copied URL to clipboard") {
		if time.Now().After(deadline) {
			t.Fatalf("dialog never showed the copy:\n%s", renderedPlain(dialog))
		}
		time.Sleep(time.Millisecond)
	}
	if got := clipboard.texts(); len(got) != 1 || got[0] != authCopyURL {
		t.Fatalf("copied %q", got)
	}
	if dialog.input.Text() != "" {
		t.Fatalf("the copy key typed %q into the code input", dialog.input.Text())
	}
}

func TestAuthURLCopyLoginDialogIgnoresTheCopyKeyWithoutAnAuthURL(t *testing.T) {
	authCopyKeybindings(t)
	clipboard := &clipboardRecorder{}
	dialog := NewLoginDialogComponent(nil, "test", nil, "")
	dialog.SetCopyToClipboard(clipboard.copy, nil)
	dialog.ShowDeviceCode(OAuthDeviceCodeInfo{VerificationURI: "https://example.invalid/device", UserCode: "ABCD"})
	dialog.HandleInput("\x18")
	time.Sleep(10 * time.Millisecond)
	if got := clipboard.texts(); len(got) != 0 {
		t.Fatalf("copied %q", got)
	}
}
