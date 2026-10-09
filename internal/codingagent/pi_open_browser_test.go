package codingagent

import (
	"errors"
	"runtime"
	"slices"
	"testing"
)

// pi: packages/coding-agent/src/utils/open-browser.ts
//
// openBrowser(target) launches the platform handler without a shell, with the target as one argument: `open` on darwin, `rundll32 url.dll,FileProtocolHandler`
// on win32, `xdg-open` elsewhere (open-browser.ts:11-17). Launching is best-effort: a launcher failure must not escape (open-browser.ts:19-23).
func TestPiCodingAgentSrcUtilsOpenBrowser(t *testing.T) {
	target := "https://example.test/authorize?code=true&client_id=x&state=a|echo.INJECTED^b"
	command := browserCommand(target)
	var wantName string
	var wantArgs []string
	switch runtime.GOOS {
	case "darwin":
		wantName, wantArgs = "open", []string{target}
	case "windows":
		wantName, wantArgs = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		wantName, wantArgs = "xdg-open", []string{target}
	}
	if len(command.Args) == 0 || command.Args[0] != wantName || !slices.Equal(command.Args[1:], wantArgs) {
		t.Fatalf("launcher = %q, want %s %q (no shell, the target as one argument)", command.Args, wantName, wantArgs)
	}

	restore := openBrowser
	t.Cleanup(func() { openBrowser = restore })
	var launched []string
	openBrowser = func(target string) error {
		launched = append(launched, target)
		return errors.New("spawn xdg-open ENOENT")
	}
	OpenBrowser(target) // the launcher failure is swallowed: callers still present the target
	if !slices.Equal(launched, []string{target}) {
		t.Fatalf("launched %q, want the target once", launched)
	}
}
