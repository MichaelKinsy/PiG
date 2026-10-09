package extensionconformance

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's chain finishes a transformer body before it starts the next one (markdown-transform.ts:18-29). The host abandons a request
// that reports no progress for five seconds (D56); the extension's body then still runs, and no later generation may enter the
// extension until it returns. The Python and Rust fixtures run "trace:<dir>:<name>" markdown through a body that records its entry
// and, for "first", waits for <dir>/release. SDKs dispatch each request on its own thread, so the host decides.
func TestMarkdownAbandonedBodyKeepsOrderAcrossSDKs(t *testing.T) {
	t.Parallel()
	for _, tc := range allHarnessCases() {
		if tc.name != "subprocess-python" && tc.name != "subprocess-rust" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			transforms := h.runner.GetMarkdownTransformers()
			if len(transforms) == 0 {
				t.Fatal("common fixture has no Markdown transformer")
			}
			transform := transforms[0]
			dir := t.TempDir()
			if runtime.GOOS != "windows" {
				// A Windows directory starts with a drive colon; a colon in the name gives the fixtures' parsing the same input here.
				dir = filepath.Join(dir, "drive:dir")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			trace := func() string {
				data, _ := os.ReadFile(filepath.Join(dir, "trace"))
				// The Python fixture appends in text mode, which writes CRLF on Windows.
				return strings.ReplaceAll(string(data), "\r\n", "\n")
			}
			ctx := extension.MarkdownTransformContext{Context: t.Context(), MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 80}
			first := "trace:" + dir + ":first"
			firstDone := make(chan string, 1)
			go func() { firstDone <- transform(first, ctx) }()
			waitForTrace(t, trace, "entered first\n")
			// The first body reports no progress: the host gives up on it and keeps its markdown.
			select {
			case got := <-firstDone:
				if got != first {
					t.Fatalf("abandoned transform = %q; want its input", got)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the host did not abandon the first body after the renderer inactivity boundary")
			}
			second := "trace:" + dir + ":second"
			if got := transform(second, ctx); got != second {
				t.Errorf("a transform during the abandoned body = %q; want its input", got)
			}
			if got := trace(); got != "entered first\n" {
				t.Fatalf("a later chain entered the extension while the abandoned body still ran: %q", got)
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			waitForTrace(t, trace, "entered first\nreturned first\n")
			third := "trace:" + dir + ":third"
			want := "md:" + third + ":assistant:streaming=false:width=80"
			deadline := time.Now().Add(10 * time.Second)
			for {
				// The proxy re-enables the transformer once the host reads the late response.
				if got := transform(third, ctx); got == want {
					break
				} else if time.Now().After(deadline) {
					t.Fatalf("transformer stayed disabled after the body returned: %q", got)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if got := trace(); !strings.HasSuffix(got, "entered third\n") {
				t.Errorf("trace = %q; want the third body entered last", got)
			}
		})
	}
}

func waitForTrace(t *testing.T, trace func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for trace() != want {
		if time.Now().After(deadline) {
			t.Fatalf("trace = %q; want %q", trace(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
