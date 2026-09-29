package codingagent

import (
	"slices"
	"strings"
	"testing"
)

// Pi's utils/open-browser.ts never invokes a shell. On Windows it passes the target to `rundll32 url.dll,FileProtocolHandler`, because cmd.exe re-parses &, |, ^ before `start` runs. OAuth authorize URLs join their parameters with &, and device-code verification URIs come from a remote server. Stand-ins for cmd.exe and rundll32.exe record what openBrowser launches without opening a browser.
func TestOpenBrowserPassesURLToWindowsHandlerWithoutShell(t *testing.T) {
	record := installArgvRecorders(t, "cmd.exe", "rundll32.exe")
	url := "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a&response_type=code&state=x|echo.INJECTED"
	if err := openBrowser(url); err != nil {
		t.Fatal(err)
	}
	got := readArgvRecords(t, record, 1)[0]
	if !strings.EqualFold(strings.TrimSuffix(got.Name, ".exe"), "rundll32") || !slices.Equal(got.Args, []string{"url.dll,FileProtocolHandler", url}) {
		t.Fatalf("browser launcher = %s %q, want rundll32 [url.dll,FileProtocolHandler %q]", got.Name, got.Args, url)
	}
}
