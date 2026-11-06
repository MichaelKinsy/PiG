package codingagent

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's Ctrl-D handler on the startup editor (interactive-mode.ts:1014-1015) shuts down while ensureTool still downloads (1017-1022). Waiting for the download
// before the quit would leave the user with a dead Ctrl-D for as long as it takes.
func TestStartupQuitKeyEndsTheManagedToolWait(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		http.DefaultTransport = managedToolTransport(func(req *http.Request) (*http.Response, error) {
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		})
		m := &InteractiveMode{chatContainer: tui.NewContainer(), editor: tui.NewEditor(), keybindings: DefaultKeybindingsManager(), inputReadCh: make(chan inputChunk, 2)}
		m.beginStartupSubmitWindow()
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.ensureManagedTools(t.Context(), tools.NewToolsManager(dir))
		}()
		synctest.Wait()
		m.inputReadCh <- inputChunk{data: []byte("\x04")}
		<-done
		if !m.requestExit.Load() {
			t.Fatal("Ctrl-D with an empty editor during the download did not request the exit")
		}
	})
}
